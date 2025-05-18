package lifecycle

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"
)

var (
	ErrNoLifecycleInCtx = errors.New("no lifecycle in context")
	ErrShutdownTimeout  = errors.New("shutdown timed out")
	ErrForcedShutdown   = errors.New("forced shutdown")
)

type lifecycleCtxKey struct{}

type lifecycleData struct {
	closerGroups map[string][]func(context.Context) error
	registerLock sync.RWMutex
}

// Context returns a context with a lifecycle. The lifecycle holds a map of closing groups.
// The lifecycle is used to close all closers to finish before exiting a (sub-)program.
// The wait is limited to the given timeout or a terminate or an interrupt signal after the initial
// cancelation. This means closing a running program immediately from shell, requires 2 interrupts.
// The additional CancelFunc can be used the start the shutdown process from inside the program.
// The returning channel should be used to prevent main from exiting and receive the shutdown errors.
// This channel is closed when all errors from all closed goroutines are received or a preemptive shutdown is triggered.
// A preemptive shutdown will add ErrShutdownTimeout or ErrForcedShutdown to the channel depending on the trigger before
// closing the channel. See the example below:
//
//	for err := range shutdownErrsChan {
//		errors = append(errors, err)
//	}
//	if len(errors) > 0 {
//		logger.Error("one or more modules failed to shutdown properly", errors)
//	}
//	// End of main()
func Context(shutdownTimeout time.Duration) (context.Context, context.CancelFunc, <-chan error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	lc := &lifecycleData{
		closerGroups: make(map[string][]func(context.Context) error),
	}
	ctx = context.WithValue(ctx, lifecycleCtxKey{}, lc)
	shutdownErrs := make(chan error)
	go func() {
		chanLock := &sync.Mutex{}
		<-ctx.Done()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		// Allow force shutdown with a secondary signal
		force := make(chan os.Signal, 1)
		signal.Notify(force, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(force)
		go func() {
			select {
			case <-ctx.Done():
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					sendErrToChan(chanLock, shutdownErrs, ErrShutdownTimeout)
				}
				close(shutdownErrs)
			case <-force:
				cancel()
				sendErrToChan(chanLock, shutdownErrs, ErrForcedShutdown)
				close(shutdownErrs)
			}
		}()
		lc.registerLock.RLock()
		runClosers(ctx, lc.closerGroups, chanLock, shutdownErrs)
		lc.registerLock.RUnlock()

	}()
	return ctx, cancel, shutdownErrs
}

func runClosers(ctx context.Context, closerGroups map[string][]func(context.Context) error, lock *sync.Mutex, errs chan<- error) {
	wg := sync.WaitGroup{}
	for _, g := range closerGroups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, c := range slices.Backward(g) {
				err := c(ctx)
				if err != nil {
					lock.Lock()
					select {
					case <-ctx.Done():
						lock.Unlock()
						return
					case errs <- err:
						lock.Unlock()
					}
				}
			}
		}()
	}
	wg.Wait()
}

func sendErrToChan(lock *sync.Mutex, errs chan<- error, err error) {
	lock.Lock()
	errs <- err
	lock.Unlock()
}

// RegisterCloser registers a new closer to be called when the context is cancelled. Each group calls its closers
// in a separate goroutine and in reverse order.
func RegisterCloser(ctx context.Context, closer func(context.Context) error, group string) error {
	lc, ok := ctx.Value(lifecycleCtxKey{}).(*lifecycleData)
	if !ok {
		return ErrNoLifecycleInCtx
	}
	lc.registerLock.Lock()
	lc.closerGroups[group] = append(lc.closerGroups[group], closer)
	lc.registerLock.Unlock()
	return nil
}
