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
	"sync/atomic"
	"testing"
	"time"
)

//------------------------------------------------------------------------------

func TestPoolSizeAdjustment(t *testing.T) {
	pool := NewFunc(10, func(_ context.Context, _ int) (string, error) { return "foo", nil })
	if exp, act := 10, len(pool.workers); exp != act {
		t.Errorf("Wrong size of pool: %v != %v", act, exp)
	}

	pool.SetSize(10)
	if exp, act := 10, pool.GetSize(); exp != act {
		t.Errorf("Wrong size of pool: %v != %v", act, exp)
	}

	pool.SetSize(9)
	if exp, act := 9, pool.GetSize(); exp != act {
		t.Errorf("Wrong size of pool: %v != %v", act, exp)
	}

	pool.SetSize(10)
	if exp, act := 10, pool.GetSize(); exp != act {
		t.Errorf("Wrong size of pool: %v != %v", act, exp)
	}

	pool.SetSize(0)
	if exp, act := 0, pool.GetSize(); exp != act {
		t.Errorf("Wrong size of pool: %v != %v", act, exp)
	}

	pool.SetSize(10)
	if exp, act := 10, pool.GetSize(); exp != act {
		t.Errorf("Wrong size of pool: %v != %v", act, exp)
	}

	// Finally, make sure we still have actual active workers.
	result, err := pool.Process(0)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if exp, act := "foo", result; exp != act {
		t.Errorf("Wrong result: %v != %v", act, exp)
	}

	pool.Close()
	if exp, act := 0, pool.GetSize(); exp != act {
		t.Errorf("Wrong size of pool: %v != %v", act, exp)
	}
}

//------------------------------------------------------------------------------

func TestFuncJob(t *testing.T) {
	pool := NewFunc(10, func(_ context.Context, in int) (int, error) {
		return in * 2, nil
	})
	defer pool.Close()

	for i := 0; i < 10; i++ {
		ret, err := pool.Process(10)
		if err != nil {
			t.Fatalf("Process failed: %v", err)
		}
		if exp, act := 20, ret; exp != act {
			t.Errorf("Wrong result: %v != %v", act, exp)
		}
	}
}

func TestFuncJobTimed(t *testing.T) {
	pool := NewFunc(10, func(_ context.Context, in int) (int, error) {
		return in * 2, nil
	})
	defer pool.Close()

	for i := 0; i < 10; i++ {
		ret, err := pool.ProcessTimed(10, time.Millisecond)
		if err != nil {
			t.Fatalf("Failed to process: %v", err)
		}
		if exp, act := 20, ret; exp != act {
			t.Errorf("Wrong result: %v != %v", act, exp)
		}
	}
}

func TestFuncJobCtx(t *testing.T) {
	t.Run("Completes when ctx not canceled", func(t *testing.T) {
		pool := NewFunc(10, func(_ context.Context, in int) (int, error) {
			return in * 2, nil
		})
		defer pool.Close()

		for i := 0; i < 10; i++ {
			ret, err := pool.ProcessCtx(context.Background(), 10)
			if err != nil {
				t.Fatalf("Failed to process: %v", err)
			}
			if exp, act := 20, ret; exp != act {
				t.Errorf("Wrong result: %v != %v", act, exp)
			}
		}
	})

	t.Run("Returns err when ctx canceled", func(t *testing.T) {
		pool := NewFunc(1, func(_ context.Context, in int) (int, error) {
			<-time.After(time.Millisecond)
			return in * 2, nil
		})
		defer pool.Close()

		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		_, act := pool.ProcessCtx(ctx, 10)
		if exp := context.DeadlineExceeded; exp != act {
			t.Errorf("Wrong error returned: %v != %v", act, exp)
		}
	})
}

func TestCallbackJob(t *testing.T) {
	pool := NewCallback(10)
	defer pool.Close()

	var counter int32
	for i := 0; i < 10; i++ {
		ret, err := pool.Process(func() {
			atomic.AddInt32(&counter, 1)
		})
		if err != nil {
			t.Fatalf("Process failed: %v", err)
		}
		if ret != nil {
			t.Errorf("Non-nil callback response: %v", ret)
		}
	}

	ret, err := pool.Process("foo")
	if err != ErrJobNotFunc {
		t.Fatalf("Expected ErrJobNotFunc, got: %v", err)
	}
	if ret != nil {
		t.Errorf("Expected nil result, got: %v", ret)
	}

	if exp, act := int32(10), counter; exp != act {
		t.Errorf("Wrong result: %v != %v", act, exp)
	}
}

func TestTimeout(t *testing.T) {
	pool := NewFunc(1, func(_ context.Context, in int) (int, error) {
		<-time.After(time.Millisecond)
		return in * 2, nil
	})
	defer pool.Close()

	_, err := pool.ProcessTimed(10, time.Duration(1))
	if err != context.DeadlineExceeded {
		t.Errorf("Expected context.DeadlineExceeded, got: %v", err)
	}
}

func TestTimedJobsAfterClose(t *testing.T) {
	pool := NewFunc(1, func(_ context.Context, _ int) (int, error) {
		return 1, nil
	})
	pool.Close()

	_, act := pool.ProcessTimed(10, time.Duration(10*time.Millisecond))
	if exp := ErrPoolNotRunning; exp != act {
		t.Errorf("Wrong error returned: %v != %v", act, exp)
	}
}

func TestJobsAfterClose(t *testing.T) {
	pool := NewFunc(1, func(_ context.Context, _ int) (int, error) {
		return 1, nil
	})
	pool.Close()

	// Process now returns error instead of panicking
	_, err := pool.Process(10)
	if err != ErrPoolNotRunning {
		t.Errorf("Expected ErrPoolNotRunning, got: %v", err)
	}
}

func TestParallelJobs(t *testing.T) {
	nWorkers := 10

	jobGroup := sync.WaitGroup{}
	testGroup := sync.WaitGroup{}

	pool := NewFunc(nWorkers, func(_ context.Context, in int) (int, error) {
		jobGroup.Done()
		jobGroup.Wait()
		return in * 2, nil
	})
	defer pool.Close()

	for j := 0; j < 1; j++ {
		jobGroup.Add(nWorkers)
		testGroup.Add(nWorkers)

		for i := 0; i < nWorkers; i++ {
			go func() {
				ret, err := pool.Process(10)
				if err != nil {
					t.Errorf("Process failed: %v", err)
					testGroup.Done()
					return
				}
				if exp, act := 20, ret; exp != act {
					t.Errorf("Wrong result: %v != %v", act, exp)
				}
				testGroup.Done()
			}()
		}

		testGroup.Wait()
	}
}

//------------------------------------------------------------------------------

type mockWorker struct {
	blockProcChan  chan struct{}
	blockReadyChan chan struct{}
	interruptChan  chan struct{}
	terminated     bool
}

func (m *mockWorker) Process(ctx context.Context, in int) (int, error) {
	select {
	case <-m.blockProcChan:
	case <-m.interruptChan:
	}
	return in, nil
}

func (m *mockWorker) BlockUntilReady() {
	<-m.blockReadyChan
}

func (m *mockWorker) Interrupt() {
	m.interruptChan <- struct{}{}
}

func (m *mockWorker) Terminate() {
	m.terminated = true
}

func TestCustomWorker(t *testing.T) {
	pool := New(1, func() Worker[int, int] {
		return &mockWorker{
			blockProcChan:  make(chan struct{}),
			blockReadyChan: make(chan struct{}),
			interruptChan:  make(chan struct{}),
		}
	})

	worker1, ok := pool.workers[0].worker.(*mockWorker)
	if !ok {
		t.Fatal("Wrong type of worker in pool")
	}

	if worker1.terminated {
		t.Fatal("Worker started off terminated")
	}

	_, err := pool.ProcessTimed(10, time.Millisecond)
	if err != context.DeadlineExceeded {
		t.Errorf("Expected context.DeadlineExceeded, got: %v", err)
	}

	close(worker1.blockReadyChan)
	_, err = pool.ProcessTimed(10, time.Millisecond)
	if err != context.DeadlineExceeded {
		t.Errorf("Expected context.DeadlineExceeded, got: %v", err)
	}

	close(worker1.blockProcChan)
	result, err := pool.Process(10)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if exp, act := 10, result; exp != act {
		t.Errorf("Wrong result: %v != %v", act, exp)
	}

	pool.Close()
	if !worker1.terminated {
		t.Fatal("Worker was not terminated")
	}
}

//------------------------------------------------------------------------------

func BenchmarkFuncJob(b *testing.B) {
	pool := NewFunc(10, func(_ context.Context, in int) (int, error) {
		return in * 2, nil
	})
	defer pool.Close()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ret, err := pool.Process(10)
		if err != nil {
			b.Errorf("Process failed: %v", err)
		}
		if exp, act := 20, ret; exp != act {
			b.Errorf("Wrong result: %v != %v", act, exp)
		}
	}
}

func BenchmarkFuncTimedJob(b *testing.B) {
	pool := NewFunc(10, func(_ context.Context, in int) (int, error) {
		return in * 2, nil
	})
	defer pool.Close()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ret, err := pool.ProcessTimed(10, time.Second)
		if err != nil {
			b.Error(err)
		}
		if exp, act := 20, ret; exp != act {
			b.Errorf("Wrong result: %v != %v", act, exp)
		}
	}
}

//------------------------------------------------------------------------------

func TestProcessAsync(t *testing.T) {
	pool := NewFunc(2, func(_ context.Context, in int) (int, error) {
		time.Sleep(10 * time.Millisecond)
		return in * 2, nil
	})
	defer pool.Close()

	// Submit 5 async jobs
	results := make([]*AsyncResult[int], 5)
	for i := 0; i < 5; i++ {
		results[i] = pool.ProcessAsync(i + 1)
	}

	// Wait for all results
	for i, asyncResult := range results {
		result, err := asyncResult.Wait()
		if err != nil {
			t.Errorf("Async job %d failed: %v", i, err)
		}
		expected := (i + 1) * 2
		if result != expected {
			t.Errorf("Wrong result for job %d: expected %d, got %d", i, expected, result)
		}
	}
}

func TestProcessBatch(t *testing.T) {
	pool := NewFunc(3, func(_ context.Context, in int) (int, error) {
		time.Sleep(5 * time.Millisecond)
		return in * 2, nil
	})
	defer pool.Close()

	payloads := []int{1, 2, 3, 4, 5}
	results, errors := pool.ProcessBatch(payloads)

	if len(results) != len(payloads) {
		t.Errorf("Wrong number of results: expected %d, got %d", len(payloads), len(results))
	}

	for i, result := range results {
		if errors[i] != nil {
			t.Errorf("Batch job %d failed: %v", i, errors[i])
		}
		expected := payloads[i] * 2
		if result != expected {
			t.Errorf("Wrong result for job %d: expected %d, got %d", i, expected, result)
		}
	}
}

func TestProcessBatchCtx(t *testing.T) {
	t.Run("All jobs complete successfully", func(t *testing.T) {
		pool := NewFunc(3, func(_ context.Context, in int) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return in * 2, nil
		})
		defer pool.Close()

		ctx := context.Background()
		payloads := []int{1, 2, 3, 4, 5}
		results, errors := pool.ProcessBatchCtx(ctx, payloads)

		if len(results) != len(payloads) {
			t.Errorf("Wrong number of results: expected %d, got %d", len(payloads), len(results))
		}

		for i, result := range results {
			if errors[i] != nil {
				t.Errorf("Batch job %d failed: %v", i, errors[i])
			}
			expected := payloads[i] * 2
			if result != expected {
				t.Errorf("Wrong result for job %d: expected %d, got %d", i, expected, result)
			}
		}
	})

	t.Run("Context cancelled during processing", func(t *testing.T) {
		pool := NewFunc(1, func(_ context.Context, in int) (int, error) {
			time.Sleep(100 * time.Millisecond)
			return in * 2, nil
		})
		defer pool.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		payloads := []int{1, 2, 3}
		_, errors := pool.ProcessBatchCtx(ctx, payloads)

		// At least some jobs should have context errors
		hasContextErr := false
		for _, err := range errors {
			if err != nil && (err == context.DeadlineExceeded || err == context.Canceled) {
				hasContextErr = true
				break
			}
		}

		if !hasContextErr {
			t.Error("Expected at least one context error when context is cancelled")
		}
	})
}

func TestPanicRecovery(t *testing.T) {
	t.Run("Worker panic is recovered and returns error", func(t *testing.T) {
		pool := NewFunc(2, func(_ context.Context, shouldPanic bool) (string, error) {
			if shouldPanic {
				panic("intentional panic for testing")
			}
			return "success", nil
		})
		defer pool.Close()

		// Test that panic is caught and error returned
		result, err := pool.Process(true)
		if err != ErrWorkerPanic {
			t.Errorf("Expected ErrWorkerPanic, got: %v", err)
		}
		if result != "" {
			t.Errorf("Expected empty result on panic, got: %v", result)
		}

		// Test that the pool still works after a panic
		result, err = pool.Process(false)
		if err != nil {
			t.Errorf("Expected no error for normal job, got: %v", err)
		}
		if result != "success" {
			t.Errorf("Expected 'success', got: %v", result)
		}
	})

	t.Run("Multiple panics don't crash the pool", func(t *testing.T) {
		pool := NewFunc(2, func(_ context.Context, n int) (int, error) {
			if n%2 == 0 {
				panic("even number panic")
			}
			return n * 2, nil
		})
		defer pool.Close()

		// Process multiple jobs that panic
		for i := 0; i < 10; i++ {
			result, err := pool.Process(i)
			if i%2 == 0 {
				// Even numbers should panic
				if err != ErrWorkerPanic {
					t.Errorf("Job %d: Expected ErrWorkerPanic, got: %v", i, err)
				}
			} else {
				// Odd numbers should succeed
				if err != nil {
					t.Errorf("Job %d: Expected no error, got: %v", i, err)
				}
				if result != i*2 {
					t.Errorf("Job %d: Expected %d, got: %d", i, i*2, result)
				}
			}
		}
	})

	t.Run("Panic in ProcessTimed returns error", func(t *testing.T) {
		pool := NewFunc(2, func(_ context.Context, shouldPanic bool) (string, error) {
			if shouldPanic {
				panic("panic in timed job")
			}
			return "success", nil
		})
		defer pool.Close()

		result, err := pool.ProcessTimed(true, 1*time.Second)
		if err != ErrWorkerPanic {
			t.Errorf("Expected ErrWorkerPanic, got: %v", err)
		}
		if result != "" {
			t.Errorf("Expected empty result on panic, got: %v", result)
		}
	})

	t.Run("Panic in ProcessCtx returns error", func(t *testing.T) {
		pool := NewFunc(2, func(_ context.Context, shouldPanic bool) (string, error) {
			if shouldPanic {
				panic("panic in ctx job")
			}
			return "success", nil
		})
		defer pool.Close()

		ctx := context.Background()
		result, err := pool.ProcessCtx(ctx, true)
		if err != ErrWorkerPanic {
			t.Errorf("Expected ErrWorkerPanic, got: %v", err)
		}
		if result != "" {
			t.Errorf("Expected empty result on panic, got: %v", result)
		}
	})

	t.Run("Panic in batch processing", func(t *testing.T) {
		pool := NewFunc(3, func(_ context.Context, n int) (int, error) {
			if n == 5 {
				panic("panic at 5")
			}
			return n * 2, nil
		})
		defer pool.Close()

		payloads := []int{1, 2, 5, 7, 9}
		results, errors := pool.ProcessBatch(payloads)

		// Check job at index 2 (value 5) panicked
		if errors[2] != ErrWorkerPanic {
			t.Errorf("Expected ErrWorkerPanic for job 5, got: %v", errors[2])
		}

		// Check other jobs succeeded
		for i, expected := range []int{2, 4, 0, 14, 18} {
			if i == 2 {
				continue // Skip the panicked job
			}
			if errors[i] != nil {
				t.Errorf("Job %d: Expected no error, got: %v", i, errors[i])
			}
			if results[i] != expected {
				t.Errorf("Job %d: Expected %d, got: %d", i, expected, results[i])
			}
		}
	})
}

// intIntHooks implements Hooks[int, int] for testing
type intIntHooks struct {
	jobStarts     atomic.Int64
	jobCompletes  atomic.Int64
	jobErrors     atomic.Int64
	workerStarts  atomic.Int64
	workerStops   atomic.Int64
	workerIDs     sync.Map
	completedJobs sync.Map
	erroredJobs   sync.Map
	t             *testing.T
}

func (h *intIntHooks) OnJobStart(payload int) {
	h.jobStarts.Add(1)
}

func (h *intIntHooks) OnJobComplete(payload int, result int, duration time.Duration) {
	h.jobCompletes.Add(1)
	h.completedJobs.Store(payload, result)
	if duration < 0 {
		h.t.Errorf("Invalid duration: %v", duration)
	}
}

func (h *intIntHooks) OnJobError(payload int, err error, duration time.Duration) {
	h.jobErrors.Add(1)
	h.erroredJobs.Store(payload, err)
	if duration < 0 {
		h.t.Errorf("Invalid duration: %v", duration)
	}
}

func (h *intIntHooks) OnWorkerStart(workerID int) {
	h.workerStarts.Add(1)
	h.workerIDs.Store(workerID, true)
}

func (h *intIntHooks) OnWorkerStop(workerID int) {
	h.workerStops.Add(1)
}

// boolStringHooks implements Hooks[bool, string] for testing
type boolStringHooks struct {
	jobStarts    atomic.Int64
	jobCompletes atomic.Int64
	jobErrors    atomic.Int64
	workerStarts atomic.Int64
	workerStops  atomic.Int64
	erroredJobs  sync.Map
}

func (h *boolStringHooks) OnJobStart(_ bool) {
	h.jobStarts.Add(1)
}

func (h *boolStringHooks) OnJobComplete(_ bool, _ string, _ time.Duration) {
	h.jobCompletes.Add(1)
}

func (h *boolStringHooks) OnJobError(payload bool, err error, _ time.Duration) {
	h.jobErrors.Add(1)
	h.erroredJobs.Store(payload, err)
}

func (h *boolStringHooks) OnWorkerStart(_ int) {
	h.workerStarts.Add(1)
}

func (h *boolStringHooks) OnWorkerStop(_ int) {
	h.workerStops.Add(1)
}

func TestHooks(t *testing.T) {
	t.Run("Hooks are called for successful jobs", func(t *testing.T) {
		hooks := &intIntHooks{t: t}
		pool := NewFunc(2, func(_ context.Context, n int) (int, error) {
			return n * 2, nil
		})
		pool.SetHooks(hooks)
		defer pool.Close()

		// Process some jobs
		for i := 0; i < 5; i++ {
			result, err := pool.Process(i)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if result != i*2 {
				t.Errorf("Expected %d, got %d", i*2, result)
			}
		}

		// Verify hooks were called
		if hooks.jobStarts.Load() != 5 {
			t.Errorf("Expected 5 job starts, got %d", hooks.jobStarts.Load())
		}
		if hooks.jobCompletes.Load() != 5 {
			t.Errorf("Expected 5 job completes, got %d", hooks.jobCompletes.Load())
		}
		if hooks.jobErrors.Load() != 0 {
			t.Errorf("Expected 0 job errors, got %d", hooks.jobErrors.Load())
		}
		// Note: OnWorkerStart is called in worker goroutine when it starts,
		// which happened before we set hooks, so we don't check workerStarts here

		// Verify completed jobs
		for i := 0; i < 5; i++ {
			val, ok := hooks.completedJobs.Load(i)
			if !ok {
				t.Errorf("Job %d not found in completed jobs", i)
			} else if val.(int) != i*2 {
				t.Errorf("Job %d: expected result %d, got %d", i, i*2, val.(int))
			}
		}
	})

	t.Run("Hooks are called for panicking jobs", func(t *testing.T) {
		hooks2 := &boolStringHooks{}
		pool := NewFunc(2, func(_ context.Context, shouldPanic bool) (string, error) {
			if shouldPanic {
				panic("test panic")
			}
			return "success", nil
		})
		pool.SetHooks(hooks2)
		defer pool.Close()

		// Process jobs with panics
		_, err := pool.Process(true)
		if err != ErrWorkerPanic {
			t.Errorf("Expected ErrWorkerPanic, got %v", err)
		}

		_, err = pool.Process(false)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}

		// Verify hooks
		if hooks2.jobStarts.Load() != 2 {
			t.Errorf("Expected 2 job starts, got %d", hooks2.jobStarts.Load())
		}
		if hooks2.jobCompletes.Load() != 1 {
			t.Errorf("Expected 1 job complete, got %d", hooks2.jobCompletes.Load())
		}
		if hooks2.jobErrors.Load() != 1 {
			t.Errorf("Expected 1 job error, got %d", hooks2.jobErrors.Load())
		}
	})

	t.Run("Worker lifecycle hooks", func(t *testing.T) {
		hooks3 := &intIntHooks{t: t}
		// Create pool with 0 workers initially, then set hooks, then scale up
		pool := NewFunc(0, func(_ context.Context, n int) (int, error) { return n, nil })
		pool.SetHooks(hooks3)

		// Scale to 3 workers
		pool.SetSize(3)
		time.Sleep(10 * time.Millisecond) // Give workers time to start
		if hooks3.workerStarts.Load() != 3 {
			t.Errorf("Expected 3 worker starts, got %d", hooks3.workerStarts.Load())
		}

		// Scale up to 5
		pool.SetSize(5)
		time.Sleep(10 * time.Millisecond) // Give workers time to start
		if hooks3.workerStarts.Load() != 5 {
			t.Errorf("Expected 5 worker starts after scale up, got %d", hooks3.workerStarts.Load())
		}

		// Scale down to 2
		pool.SetSize(2)
		time.Sleep(10 * time.Millisecond) // Give workers time to stop
		if hooks3.workerStops.Load() != 3 {
			t.Errorf("Expected 3 worker stops after scale down, got %d", hooks3.workerStops.Load())
		}

		pool.Close()
		time.Sleep(10 * time.Millisecond) // Give workers time to stop
		if hooks3.workerStops.Load() != 5 {
			t.Errorf("Expected 5 total worker stops after close, got %d", hooks3.workerStops.Load())
		}
	})
}

func TestGracefulShutdown(t *testing.T) {
	t.Run("Shutdown waits for queued jobs to complete", func(t *testing.T) {
		pool := NewFunc(2, func(_ context.Context, n int) (int, error) {
			time.Sleep(50 * time.Millisecond)
			return n * 2, nil
		})

		// Submit jobs asynchronously
		var wg sync.WaitGroup
		results := make([]int, 10)
		errors := make([]error, 10)

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				results[idx], errors[idx] = pool.Process(idx)
			}(i)
		}

		// Give some time for jobs to queue up
		time.Sleep(20 * time.Millisecond)

		// Shutdown with generous timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := pool.Shutdown(ctx)
		if err != nil {
			t.Errorf("Unexpected shutdown error: %v", err)
		}

		wg.Wait()

		// Verify all jobs completed successfully
		successCount := 0
		for i, err := range errors {
			if err == nil {
				successCount++
				if results[i] != i*2 {
					t.Errorf("Job %d: expected %d, got %d", i, i*2, results[i])
				}
			}
		}

		if successCount == 0 {
			t.Error("Expected at least some jobs to complete successfully")
		}
	})

	t.Run("Shutdown respects context cancellation", func(t *testing.T) {
		pool := NewFunc(2, func(_ context.Context, n int) (int, error) {
			time.Sleep(100 * time.Millisecond)
			return n * 2, nil
		})

		// Submit long-running jobs
		for i := 0; i < 10; i++ {
			go pool.Process(i)
		}

		// Give time for jobs to queue
		time.Sleep(20 * time.Millisecond)

		// Shutdown with very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		err := pool.Shutdown(ctx)
		if err != context.DeadlineExceeded {
			t.Errorf("Expected context.DeadlineExceeded, got: %v", err)
		}
	})

	t.Run("Shutdown with no queued jobs returns immediately", func(t *testing.T) {
		pool := NewFunc(2, func(_ context.Context, n int) (int, error) {
			return n * 2, nil
		})

		// Process and complete a job
		_, err := pool.Process(5)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		// Shutdown should return immediately since no jobs are queued
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		err = pool.Shutdown(ctx)
		duration := time.Since(start)

		if err != nil {
			t.Errorf("Unexpected shutdown error: %v", err)
		}

		if duration > 100*time.Millisecond {
			t.Errorf("Shutdown took too long: %v", duration)
		}
	})
}

func TestContextCancellation(t *testing.T) {
	t.Run("Context is cancelled on timeout", func(t *testing.T) {
		var ctxCancelled atomic.Bool

		pool := NewFunc(1, func(ctx context.Context, _ int) (int, error) {
			// Block and wait for context cancellation
			<-ctx.Done()
			ctxCancelled.Store(true)
			return 0, ctx.Err()
		})
		defer pool.Close()

		// Process with very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		_, err := pool.ProcessCtx(ctx, 42)

		if err != context.DeadlineExceeded {
			t.Errorf("Expected context.DeadlineExceeded, got: %v", err)
		}

		// Give some time for context cancellation to propagate
		time.Sleep(50 * time.Millisecond)

		if !ctxCancelled.Load() {
			t.Error("Worker context was not cancelled")
		}
	})

	t.Run("Context is cancelled on ProcessTimed", func(t *testing.T) {
		var ctxCancelled atomic.Bool

		pool := NewFunc(1, func(ctx context.Context, _ int) (int, error) {
			// Block and wait for context cancellation
			<-ctx.Done()
			ctxCancelled.Store(true)
			return 0, ctx.Err()
		})
		defer pool.Close()

		// Process with very short timeout using ProcessTimed
		_, err := pool.ProcessTimed(42, 10*time.Millisecond)

		if err != context.DeadlineExceeded {
			t.Errorf("Expected context.DeadlineExceeded, got: %v", err)
		}

		// Give some time for context cancellation to propagate
		time.Sleep(50 * time.Millisecond)

		if !ctxCancelled.Load() {
			t.Error("Worker context was not cancelled on ProcessTimed timeout")
		}
	})
}

//------------------------------------------------------------------------------
