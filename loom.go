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
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

//------------------------------------------------------------------------------

// Errors that are used throughout the Loomy API.
var (
	ErrPoolNotRunning = errors.New("the pool is not running")
	ErrJobNotFunc     = errors.New("generic worker not given a func()")
	ErrWorkerClosed   = errors.New("worker was closed")
	ErrJobTimedOut    = errors.New("job request timed out")
	ErrWorkerPanic    = errors.New("worker panicked during job processing")
)

// Hooks provides callbacks for monitoring and instrumenting pool behavior.
// All hooks are optional - nil hooks will be skipped.
type Hooks[In, Out any] interface {
	// OnJobStart is called when a job begins processing.
	OnJobStart(payload In)

	// OnJobComplete is called when a job finishes successfully.
	OnJobComplete(payload In, result Out, duration time.Duration)

	// OnJobError is called when a job fails with an error.
	OnJobError(payload In, err error, duration time.Duration)

	// OnWorkerStart is called when a new worker starts.
	OnWorkerStart(workerID int)

	// OnWorkerStop is called when a worker is stopped.
	OnWorkerStop(workerID int)
}

// Worker is an interface representing a Loomy working agent with generic type parameters.
// It will be used to block a calling goroutine until ready to process a job, process that job
// synchronously, interrupt its own process call when jobs are abandoned, and
// clean up its resources when being removed from the pool.
//
// Each of these duties are implemented as a single method and can be averted
// when not needed by simply implementing an empty func.
type Worker[In, Out any] interface {
	// Process will synchronously perform a job and return the result or an error.
	// The context can be used to cancel the job or set timeouts.
	Process(ctx context.Context, payload In) (Out, error)

	// BlockUntilReady is called before each job is processed and must block the
	// calling goroutine until the Worker is ready to process the next job.
	BlockUntilReady()

	// Interrupt is called when a job is cancelled. The worker is responsible
	// for unblocking the Process implementation.
	Interrupt()

	// Terminate is called when a Worker is removed from the processing pool
	// and is responsible for cleaning up any held resources.
	Terminate()
}

// WorkerAny is a backward-compatible alias for Worker using interface{} types.
// Deprecated: Use Worker[In, Out] with specific types instead.
type WorkerAny = Worker[interface{}, interface{}]

//------------------------------------------------------------------------------

// closureWorker is a minimal Worker implementation that simply wraps a
// processing function with generic type parameters.
type closureWorker[In, Out any] struct {
	processor func(context.Context, In) (Out, error)
}

func (w *closureWorker[In, Out]) Process(ctx context.Context, payload In) (Out, error) {
	return w.processor(ctx, payload)
}

func (w *closureWorker[In, Out]) BlockUntilReady() {}
func (w *closureWorker[In, Out]) Interrupt()       {}
func (w *closureWorker[In, Out]) Terminate()       {}

//------------------------------------------------------------------------------

// callbackWorker is a minimal Worker implementation that attempts to cast
// each job into func() and either calls it if successful or returns
// ErrJobNotFunc.
type callbackWorker struct{}

func (w *callbackWorker) Process(_ context.Context, payload interface{}) (interface{}, error) {
	f, ok := payload.(func())
	if !ok {
		return nil, ErrJobNotFunc
	}
	f()
	return nil, nil
}

func (w *callbackWorker) BlockUntilReady() {}
func (w *callbackWorker) Interrupt()       {}
func (w *callbackWorker) Terminate()       {}

//------------------------------------------------------------------------------

// AsyncResult represents a job result that can be retrieved asynchronously.
type AsyncResult[Out any] struct {
	result Out
	err    error
	done   chan struct{}
}

// Wait blocks until the job is complete and returns the result and error.
func (ar *AsyncResult[Out]) Wait() (Out, error) {
	<-ar.done
	return ar.result, ar.err
}

// Done returns a channel that will be closed when the job is complete.
func (ar *AsyncResult[Out]) Done() <-chan struct{} {
	return ar.done
}

//------------------------------------------------------------------------------

// Pool is a struct that manages a collection of workers with generic type parameters,
// each with their own goroutine. The Pool can initialize, expand, compress and close
// the workers, as well as processing jobs with the workers synchronously.
type Pool[In, Out any] struct {
	// queuedJobs is first for proper alignment on 32-bit systems
	queuedJobs atomic.Int64

	ctor    func() Worker[In, Out]
	workers []*workerWrapper[In, Out]
	reqChan chan workRequest[In, Out]
	hooks   Hooks[In, Out]

	workerMut    sync.Mutex
	nextWorkerID atomic.Int64
}

// PoolAny is a backward-compatible alias for Pool using interface{} types.
// Deprecated: Use Pool[In, Out] with specific types instead.
type PoolAny = Pool[interface{}, interface{}]

// New creates a new Pool of workers that starts with n workers. You must
// provide a constructor function that creates new Worker types and when you
// change the size of the pool the constructor will be called to create each new
// Worker.
func New[In, Out any](n int, ctor func() Worker[In, Out]) *Pool[In, Out] {
	p := &Pool[In, Out]{
		ctor:    ctor,
		reqChan: make(chan workRequest[In, Out]),
	}
	p.SetSize(n)

	return p
}

// NewFunc creates a new Pool of workers where each worker will process using
// the provided func. The function receives a context for cancellation support.
func NewFunc[In, Out any](n int, f func(context.Context, In) (Out, error)) *Pool[In, Out] {
	return New(n, func() Worker[In, Out] {
		return &closureWorker[In, Out]{
			processor: f,
		}
	})
}

// NewCallback creates a new Pool of workers where workers cast the job payload
// into a func() and runs it, or returns ErrNotFunc if the cast failed.
// This is a backward-compatible function using interface{} types.
func NewCallback(n int) *Pool[interface{}, interface{}] {
	return New(n, func() Worker[interface{}, interface{}] {
		return &callbackWorker{}
	})
}

//------------------------------------------------------------------------------

// Process will use the Pool to process a payload and synchronously return the
// result. Process can be called safely by any goroutines, and returns an error
// if the Pool has been stopped. Uses context.Background() for the job context.
func (p *Pool[In, Out]) Process(payload In) (Out, error) {
	return p.ProcessCtx(context.Background(), payload)
}

// ProcessTimed will use the Pool to process a payload and synchronously return
// the result. If the timeout occurs before the job has finished the worker will
// be interrupted and ErrJobTimedOut will be returned. ProcessTimed can be
// called safely by any goroutines.
func (p *Pool[In, Out]) ProcessTimed(
	payload In,
	timeout time.Duration,
) (Out, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return p.ProcessCtx(ctx, payload)
}

// ProcessCtx will use the Pool to process a payload and synchronously return
// the result. If the context cancels before the job has finished the worker will
// be interrupted and the context error will be returned. ProcessCtx can be
// called safely by any goroutines.
func (p *Pool[In, Out]) ProcessCtx(ctx context.Context, payload In) (Out, error) {
	p.queuedJobs.Add(1)
	defer p.queuedJobs.Add(-1)

	var request workRequest[In, Out]
	var open bool
	var zero Out

	select {
	case request, open = <-p.reqChan:
		if !open {
			return zero, ErrPoolNotRunning
		}
	case <-ctx.Done():
		return zero, ctx.Err()
	}

	// Send both payload and context
	select {
	case request.jobChan <- payload:
	case <-ctx.Done():
		request.interruptFunc()
		return zero, ctx.Err()
	}

	select {
	case request.ctxChan <- ctx:
	case <-ctx.Done():
		request.interruptFunc()
		return zero, ctx.Err()
	}

	select {
	case result, open := <-request.retChan:
		if !open {
			return zero, ErrWorkerClosed
		}
		return result, nil
	case err := <-request.errChan:
		return zero, err
	case <-ctx.Done():
		request.interruptFunc()
		return zero, ctx.Err()
	}
}

// ProcessAsync submits a job for asynchronous processing and returns an AsyncResult.
// Use the returned AsyncResult to wait for completion or check if it's done.
func (p *Pool[In, Out]) ProcessAsync(payload In) *AsyncResult[Out] {
	result := &AsyncResult[Out]{
		done: make(chan struct{}),
	}

	go func() {
		defer close(result.done)
		result.result, result.err = p.Process(payload)
	}()

	return result
}

// ProcessBatch processes multiple payloads concurrently using the pool and waits
// for all jobs to complete. It returns slices of results and errors in the same
// order as the input payloads.
func (p *Pool[In, Out]) ProcessBatch(payloads []In) ([]Out, []error) {
	results := make([]Out, len(payloads))
	errs := make([]error, len(payloads))
	var wg sync.WaitGroup

	for i, payload := range payloads {
		wg.Add(1)
		go func(index int, pl In) {
			defer wg.Done()
			results[index], errs[index] = p.Process(pl)
		}(i, payload)
	}

	wg.Wait()
	return results, errs
}

// ProcessBatchCtx processes multiple payloads concurrently using the pool with context
// support. It waits for all jobs to complete or until the context is cancelled.
// Returns slices of results and errors in the same order as the input payloads.
func (p *Pool[In, Out]) ProcessBatchCtx(ctx context.Context, payloads []In) ([]Out, []error) {
	results := make([]Out, len(payloads))
	errs := make([]error, len(payloads))
	var wg sync.WaitGroup

	for i, payload := range payloads {
		wg.Add(1)
		go func(index int, pl In) {
			defer wg.Done()
			results[index], errs[index] = p.ProcessCtx(ctx, pl)
		}(i, payload)
	}

	wg.Wait()
	return results, errs
}

// QueueLength returns the current count of pending queued jobs.
func (p *Pool[In, Out]) QueueLength() int64 {
	return p.queuedJobs.Load()
}

// SetSize changes the total number of workers in the Pool. This can be called
// by any goroutine at any time unless the Pool has been stopped.
func (p *Pool[In, Out]) SetSize(n int) {
	p.workerMut.Lock()
	defer p.workerMut.Unlock()

	lWorkers := len(p.workers)
	if lWorkers == n {
		return
	}

	// Add extra workers if N > len(workers)
	for i := lWorkers; i < n; i++ {
		workerID := int(p.nextWorkerID.Add(1))
		p.workers = append(p.workers, newWorkerWrapper(p.reqChan, p.ctor(), workerID, p.hooks))
	}

	// Asynchronously stop all workers > N
	for i := n; i < lWorkers; i++ {
		p.workers[i].stop()
	}

	// Synchronously wait for all workers > N to stop
	for i := n; i < lWorkers; i++ {
		p.workers[i].join()
		p.workers[i] = nil
	}

	// Remove stopped workers from slice
	p.workers = p.workers[:n]
}

// GetSize returns the current size of the pool.
func (p *Pool[In, Out]) GetSize() int {
	p.workerMut.Lock()
	defer p.workerMut.Unlock()

	return len(p.workers)
}

// SetHooks sets the hooks for monitoring pool and worker lifecycle events.
// Pass nil to disable hooks. Note: hooks are only applied to new workers created
// after this call. Existing workers retain their old hooks.
func (p *Pool[In, Out]) SetHooks(hooks Hooks[In, Out]) {
	p.workerMut.Lock()
	defer p.workerMut.Unlock()
	p.hooks = hooks

	// Update hooks on existing workers
	for _, worker := range p.workers {
		worker.hooksMut.Lock()
		worker.hooks = hooks
		worker.hooksMut.Unlock()
	}
}

// Close will terminate all workers and close the job channel of this Pool.
func (p *Pool[In, Out]) Close() {
	p.SetSize(0)
	close(p.reqChan)
}

// Shutdown gracefully shuts down the pool, waiting for all queued jobs to complete
// before terminating workers. Returns when all workers have stopped.
// If the context is cancelled, shutdown proceeds immediately without waiting for jobs.
func (p *Pool[In, Out]) Shutdown(ctx context.Context) error {
	// Wait for all queued jobs to complete
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Context cancelled, proceed with immediate shutdown
			p.Close()
			return ctx.Err()
		case <-ticker.C:
			if p.QueueLength() == 0 {
				// No more queued jobs, safe to close
				p.Close()
				return nil
			}
		}
	}
}

//------------------------------------------------------------------------------
