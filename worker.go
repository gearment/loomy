// Copyright (c) 2025 Gearment LLC.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package loomy

import (
	"context"
	"sync"
	"time"
)

//------------------------------------------------------------------------------

// workRequest is a struct containing context representing a workers intention
// to receive a work payload with generic type parameters.
type workRequest[In, Out any] struct {
	// jobChan is used to send the payload to this worker.
	jobChan chan<- In

	// ctxChan is used to send the context for this job.
	ctxChan chan<- context.Context

	// retChan is used to read the result from this worker.
	retChan <-chan Out

	// errChan is used to read any error (including panics) from this worker.
	errChan <-chan error

	// interruptFunc can be called to cancel a running job. When called it is no
	// longer necessary to read from retChan.
	interruptFunc func()
}

//------------------------------------------------------------------------------

// workerWrapper takes a Worker implementation and wraps it within a goroutine
// and channel arrangement. The workerWrapper is responsible for managing the
// lifetime of both the Worker and the goroutine.
type workerWrapper[In, Out any] struct {
	worker        Worker[In, Out]
	interruptChan chan struct{}
	workerID      int
	hooks         Hooks[In, Out]

	// reqChan is NOT owned by this type, it is used to send requests for work.
	reqChan chan<- workRequest[In, Out]

	// closeChan can be closed in order to cleanly shutdown this worker.
	closeChan chan struct{}

	// closedChan is closed by the run() goroutine when it exits.
	closedChan chan struct{}

	// cancelFunc cancels the current job's context when interrupted
	cancelFunc context.CancelFunc
	cancelMut  sync.Mutex

	// hooksMut protects concurrent access to hooks
	hooksMut sync.RWMutex
}

func newWorkerWrapper[In, Out any](
	reqChan chan<- workRequest[In, Out],
	worker Worker[In, Out],
	workerID int,
	hooks Hooks[In, Out],
) *workerWrapper[In, Out] {
	w := workerWrapper[In, Out]{
		worker:        worker,
		interruptChan: make(chan struct{}),
		workerID:      workerID,
		hooks:         hooks,
		reqChan:       reqChan,
		closeChan:     make(chan struct{}),
		closedChan:    make(chan struct{}),
	}

	go w.run()

	return &w
}

//------------------------------------------------------------------------------

func (w *workerWrapper[In, Out]) interrupt() {
	// Cancel the context if there's an active job
	w.cancelMut.Lock()
	if w.cancelFunc != nil {
		w.cancelFunc()
	}
	w.cancelMut.Unlock()

	close(w.interruptChan)
	w.worker.Interrupt()
}

func (w *workerWrapper[In, Out]) run() {
	jobChan, ctxChan, retChan, errChan := make(chan In), make(chan context.Context), make(chan Out), make(chan error)

	// Call OnWorkerStart hook
	w.hooksMut.RLock()
	if w.hooks != nil {
		w.hooks.OnWorkerStart(w.workerID)
	}
	w.hooksMut.RUnlock()

	defer func() {
		w.worker.Terminate()

		// Call OnWorkerStop hook
		w.hooksMut.RLock()
		if w.hooks != nil {
			w.hooks.OnWorkerStop(w.workerID)
		}
		w.hooksMut.RUnlock()

		close(retChan)
		close(errChan)
		close(w.closedChan)
	}()

	for {
		// NOTE: Blocking here will prevent the worker from closing down.
		w.worker.BlockUntilReady()
		select {
		case w.reqChan <- workRequest[In, Out]{
			jobChan:       jobChan,
			ctxChan:       ctxChan,
			retChan:       retChan,
			errChan:       errChan,
			interruptFunc: w.interrupt,
		}:
			select {
			case payload := <-jobChan:
				// Wait for context
				parentCtx := <-ctxChan

				// Create a cancellable context that we can cancel on interrupt
				ctx, cancel := context.WithCancel(parentCtx)
				w.cancelMut.Lock()
				w.cancelFunc = cancel
				w.cancelMut.Unlock()

				// Call OnJobStart hook
				w.hooksMut.RLock()
				if w.hooks != nil {
					w.hooks.OnJobStart(payload)
				}
				w.hooksMut.RUnlock()

				// Track job duration
				startTime := time.Now()

				// Process with panic recovery
				var result Out
				var processErr error

				func() {
					defer func() {
						if r := recover(); r != nil {
							processErr = ErrWorkerPanic
						}
					}()
					result, processErr = w.worker.Process(ctx, payload)
				}()

				// Clean up cancel function
				w.cancelMut.Lock()
				w.cancelFunc = nil
				w.cancelMut.Unlock()
				cancel() // Always cancel to free resources

				duration := time.Since(startTime)

				// Send error if panic or error occurred
				if processErr != nil {
					// Call OnJobError hook
					w.hooksMut.RLock()
					if w.hooks != nil {
						w.hooks.OnJobError(payload, processErr, duration)
					}
					w.hooksMut.RUnlock()

					select {
					case errChan <- processErr:
					case <-w.interruptChan:
						w.interruptChan = make(chan struct{})
					}
				} else {
					// Call OnJobComplete hook
					w.hooksMut.RLock()
					if w.hooks != nil {
						w.hooks.OnJobComplete(payload, result, duration)
					}
					w.hooksMut.RUnlock()

					// Send result if no error
					select {
					case retChan <- result:
					case <-w.interruptChan:
						w.interruptChan = make(chan struct{})
					}
				}
			case <-w.interruptChan:
				w.interruptChan = make(chan struct{})
			}
		case <-w.closeChan:
			return
		}
	}
}

//------------------------------------------------------------------------------

func (w *workerWrapper[In, Out]) stop() {
	close(w.closeChan)
}

func (w *workerWrapper[In, Out]) join() {
	<-w.closedChan
}

//------------------------------------------------------------------------------
