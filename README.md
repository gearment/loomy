<div align="center">
  <img src="logo.svg" alt="Loomy Logo" width="200"/>

  # Loomy - Modern Go Worker Pool

  [![Go Version](https://img.shields.io/badge/Go-%3E%3D1.19-00ADD8?logo=go)](https://go.dev/)
  [![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

</div>

Loomy is a Golang library for spawning and managing a goroutine pool with **type-safe generics** and advanced batch processing capabilities.

A fixed goroutine pool is helpful when you have work coming from an arbitrary number of asynchronous sources, but a limited capacity for parallel processing. For example, when processing jobs from HTTP requests that are CPU heavy, you can create a pool with a size that matches your CPU count.

## Features

- ✅ **Type-Safe Generics** - Full generic support with compile-time type safety
- ✅ **Panic Recovery** - Automatic panic recovery prevents pool crashes
- ✅ **Hooks System** - Monitor worker lifecycle and job events
- ✅ **Graceful Shutdown** - Wait for queued jobs before termination
- ✅ **Batch Processing** - Process multiple jobs concurrently with `ProcessBatch`
- ✅ **Async Processing** - Non-blocking job submission with `ProcessAsync`
- ✅ **Context Support** - First-class context.Context integration
- ✅ **Error Handling** - Idiomatic error returns instead of panics
- ✅ **Zero Dependencies** - Pure Go standard library
- ✅ **Production Ready** - 95%+ test coverage

## Install

```sh
go get github.com/gearment/loomy
```

Requires Go 1.19+ (uses `atomic.Int64` and generics)

## Quick Start

### Basic Usage with Generics

```go
package main

import (
	"context"
	"fmt"
	"runtime"

	"github.com/gearment/loomy"
)

func main() {
	numCPUs := runtime.NumCPU()

	// Create a type-safe pool
	pool := loomy.NewFunc(numCPUs, func(ctx context.Context, n int) (int, error) {
		// CPU-intensive work here
		return n * n, nil
	})
	defer pool.Close()

	// Process jobs with type safety
	result, err := pool.Process(5)
	if err != nil {
		panic(err)
	}
	fmt.Println(result) // Output: 25
}
```

### HTTP Server Example

```go
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"runtime"

	"github.com/gearment/loomy"
)

type Request struct {
	Data string `json:"data"`
}

type Response struct {
	Result string `json:"result"`
}

func main() {
	numCPUs := runtime.NumCPU()

	pool := loomy.NewFunc(numCPUs, func(ctx context.Context, req Request) (Response, error) {
		// CPU-intensive processing
		result := processData(req.Data)
		return Response{Result: result}, nil
	})
	defer pool.Close()

	http.HandleFunc("/work", func(w http.ResponseWriter, r *http.Request) {
		var req Request
		body, _ := io.ReadAll(r.Body)
		defer r.Body.Close()

		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Process with context from request
		result, err := pool.ProcessCtx(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(result)
	})

	http.ListenAndServe(":8080", nil)
}

func processData(data string) string {
	// Your CPU-intensive work here
	return "processed: " + data
}
```

## Advanced Features

### Batch Processing

Process multiple jobs concurrently and wait for all to complete:

```go
pool := loomy.NewFunc(10, func(ctx context.Context, n int) (int, error) {
	return n * 2, nil
})
defer pool.Close()

payloads := []int{1, 2, 3, 4, 5}
results, errors := pool.ProcessBatch(payloads)

for i, result := range results {
	if errors[i] != nil {
		fmt.Printf("Job %d failed: %v\n", i, errors[i])
		continue
	}
	fmt.Printf("Job %d result: %d\n", i, result)
}
```

### Batch Processing with Context

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

results, errors := pool.ProcessBatchCtx(ctx, payloads)
// Jobs will be cancelled if context times out
```

### Async Processing

Submit jobs without blocking:

```go
// Submit async job
asyncResult := pool.ProcessAsync(42)

// Do other work...

// Wait for result when needed
result, err := asyncResult.Wait()
```

### Timeout Support

```go
result, err := pool.ProcessTimed(payload, 5*time.Second)
if err == loomy.ErrJobTimedOut {
	fmt.Println("Job timed out!")
}
```

### Context-Based Cancellation

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

result, err := pool.ProcessCtx(ctx, payload)
if err == context.DeadlineExceeded {
	fmt.Println("Request timed out")
}
```

## Panic Recovery

Workers automatically recover from panics, preventing pool crashes:

```go
pool := loomy.NewFunc(5, func(ctx context.Context, n int) (int, error) {
	if n < 0 {
		panic("negative number!")
	}
	return n * 2, nil
})
defer pool.Close()

result, err := pool.Process(-5)
if err == loomy.ErrWorkerPanic {
	fmt.Println("Job panicked, but pool is still healthy")
}

// Pool continues to work normally
result, err = pool.Process(10) // Works fine: result = 20
```

## Graceful Shutdown

Wait for all queued jobs to complete before shutdown:

```go
pool := loomy.NewFunc(10, func(ctx context.Context, n int) (int, error) {
	// Process job
	return n * 2, nil
})

// Submit many jobs...
for i := 0; i < 1000; i++ {
	go pool.Process(i)
}

// Gracefully shutdown with timeout
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := pool.Shutdown(ctx); err != nil {
	fmt.Printf("Shutdown error: %v\n", err)
}
```

## Hooks and Monitoring

Instrument your pool with lifecycle hooks:

```go
type MyHooks struct{}

func (h *MyHooks) OnJobStart(payload int) {
	fmt.Printf("Job started: %d\n", payload)
}

func (h *MyHooks) OnJobComplete(payload int, result int, duration time.Duration) {
	fmt.Printf("Job %d completed in %v: %d\n", payload, duration, result)
}

func (h *MyHooks) OnJobError(payload int, err error, duration time.Duration) {
	fmt.Printf("Job %d failed after %v: %v\n", payload, duration, err)
}

func (h *MyHooks) OnWorkerStart(workerID int) {
	fmt.Printf("Worker %d started\n", workerID)
}

func (h *MyHooks) OnWorkerStop(workerID int) {
	fmt.Printf("Worker %d stopped\n", workerID)
}

pool := loomy.NewFunc(5, func(ctx context.Context, n int) (int, error) {
	return n * 2, nil
})
pool.SetHooks(&MyHooks{})
defer pool.Close()
```

## Dynamic Pool Sizing

The pool size can be changed at any time from any goroutine:

```go
pool.SetSize(10)   // Scale to 10 workers
pool.SetSize(100)  // Scale to 100 workers
pool.SetSize(5)    // Scale down to 5 workers

currentSize := pool.GetSize()
```

## Custom Workers

For advanced use cases where workers need their own state:

```go
type MyWorker struct {
	db *sql.DB
}

func (w *MyWorker) Process(ctx context.Context, query string) ([]Result, error) {
	// Use worker's database connection
	rows, err := w.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	// ... process results
	return results, nil
}

func (w *MyWorker) BlockUntilReady() {
	// Optional: block until worker is ready
}

func (w *MyWorker) Interrupt() {
	// Optional: handle job cancellation
}

func (w *MyWorker) Terminate() {
	// Cleanup when worker is removed
	w.db.Close()
}

// Create pool with custom workers
pool := loomy.New(10, func() loomy.Worker[string, []Result] {
	db, _ := sql.Open("postgres", connStr)
	return &MyWorker{db: db}
})
defer pool.Close()
```

## Migration from interface{} to Generics

### Legacy (interface{} based)
```go
pool := loomy.NewFunc(10, func(payload interface{}) interface{} {
	val := payload.(int)  // Runtime type assertion
	return val * 2
})
result := pool.Process(5).(int)  // Runtime type assertion
```

### New (type-safe generics)
```go
pool := loomy.NewFunc(10, func(ctx context.Context, val int) (int, error) {
	return val * 2, nil  // No type assertions needed!
})
result, err := pool.Process(5)  // result is type int
if err != nil {
	// Handle error
}
```

## Error Handling

Loomy uses idiomatic Go error handling:

```go
result, err := pool.Process(payload)
if err == loomy.ErrPoolNotRunning {
	// Pool was closed
} else if err == loomy.ErrWorkerClosed {
	// Worker terminated unexpectedly
} else if err == loomy.ErrWorkerPanic {
	// Worker panicked during job processing (pool remains healthy)
} else if err == loomy.ErrJobTimedOut {
	// Job exceeded timeout duration
}
```

## Performance

Benchmarks on Apple M2:

```
BenchmarkFuncJob-8        	 1134168	      1261 ns/op	      24 B/op	       1 allocs/op
BenchmarkFuncTimedJob-8   	  772045	      1419 ns/op	     272 B/op	       4 allocs/op
```

## Monitoring

Track the current queue length:

```go
queueLen := pool.QueueLength()
fmt.Printf("Jobs waiting: %d\n", queueLen)
```

## Job Ordering

Backlogged jobs are not guaranteed to be processed in order. Due to the implementation of channels and select blocks, a stack of backlogged jobs will typically be processed as a FIFO queue, but this behavior is not part of the specification and should not be relied upon.

## Architecture

See [IMPROVEMENTS.md](IMPROVEMENTS.md) for technical details:

- Generic type parameters for compile-time type safety
- Panic recovery with error channel communication
- Hooks interface for observability and monitoring
- Context-based graceful shutdown
- `atomic.Int64` for efficient atomic operations
- Memory alignment optimized for 32-bit systems
- WaitGroup-based batch processing
- Idiomatic error handling
- Resource leak prevention
- High performance with minimal allocations

## License

MIT License - See LICENSE file
