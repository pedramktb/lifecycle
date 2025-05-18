package lifecycle_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pedramktb/go-lifecycle"
	"github.com/stretchr/testify/assert"
)

func Test_Lifecycle(t *testing.T) {
	tests := []struct {
		name       string
		timeout    time.Duration
		closers    map[string][]func(context.Context) error
		nonNilErrs int
		inTime     bool
	}{
		{
			name:    "should close all closers in order",
			timeout: time.Second,
			closers: map[string][]func(context.Context) error{
				"": {
					func(context.Context) error { return errors.New("test error") },
					func(context.Context) error { return nil },
				},
			},
			nonNilErrs: 1,
			inTime:     true,
		},
		{
			name:    "should close all closers in order in their respective groups in parallel",
			timeout: time.Second,
			closers: map[string][]func(context.Context) error{
				"": {
					func(context.Context) error { return errors.New("test error") },
					func(context.Context) error { return nil },
				},
				"parallel": {
					func(context.Context) error { return errors.New("test error 2") },
					func(context.Context) error { return nil },
				},
			},
			nonNilErrs: 2,
			inTime:     true,
		},
		{
			name:    "fail to close all closers in time",
			timeout: time.Second,
			closers: map[string][]func(context.Context) error{
				"": {
					func(context.Context) error { time.Sleep(time.Second * 4); return errors.New("unreachable error") },
					func(context.Context) error { return errors.New("test error") },
				},
			},
			nonNilErrs: 1,
			inTime:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel, shutdownErrs := lifecycle.Context(tc.timeout)
			for group, closers := range tc.closers {
				for _, closer := range closers {
					err := lifecycle.RegisterCloser(ctx, closer, group)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			// cancel the context to trigger closers
			cancel()
			nonNilErrs := []error{}
			for err := range shutdownErrs {
				if err != nil {
					nonNilErrs = append(nonNilErrs, err)
				}
			}
			if tc.inTime {
				assert.Equal(t, tc.nonNilErrs, len(nonNilErrs))
			} else {
				assert.Equal(t, tc.nonNilErrs+1, len(nonNilErrs))
				assert.Error(t, nonNilErrs[len(nonNilErrs)-1], lifecycle.ErrShutdownTimeout)
			}
		})
	}
}
