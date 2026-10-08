# goshard

[![Go Reference](https://pkg.go.dev/badge/github.com/darxnet/goshard.svg)](https://pkg.go.dev/github.com/darxnet/goshard)
[![Go](https://github.com/darxnet/goshard/actions/workflows/release.yml/badge.svg)](https://github.com/darxnet/goshard/actions/workflows/release.yml)
![Coverage](https://img.shields.io/badge/Coverage-100%25-brightgreen)

`goshard` is a high-performance, concurrent-safe sharded map for Go. 
It is engineered for **1M+ RPS workloads** where minimizing GC pressure and lock contention is critical for system stability.

## Why goshard?

**Production Ready**: Battle-tested in high-load production environments, reliably handling **1M+ RPS** with stable P99 latency.

Standard `sync.Map` is excellent for read-heavy workloads with stable keys. However, in high-churn environments (frequent writes, deletions, and TTL-based evictions), `sync.Map` can become a bottleneck due to its single-writer lock and heap allocations.

`goshard` solves this by:
1.  **Eliminating Heap Allocations**: For `comparable` keys and values, `goshard` achieves **zero allocations** on most hot paths.
2.  **Reducing Contention**: Distributes load across 64+ independent shards, each with its own lock.
3.  **Predictable Latency**: By avoiding GC garbage, it prevents P99 latency spikes caused by mark-and-sweep pauses.

| Feature | `goshard.Map` | `sync.Map` |
|---|---|---|
| **Hot-path Allocations** | **Zero** | 1-3 per Store |
| **Write Lock Contention** | Distributed (N Shards) | Global (1 Mutex) |
| **Batch Deletion** | **Shard-aware Batching** | Sequential |
| **GC Pressure** | Extremely Low | High at scale |
| **Read Performance** | Fast (RLock) | **Extremely Fast (Lock-free)** |

## Features

- **Zero-Allocation Hot Path** — `Store`, `Load`, `Delete`, and `Swap` allocate nothing on the heap for `comparable` types.
- **Cache-Line Padding** — Prevents "false sharing" by ensuring shard locks don't collide on the same CPU cache line.
- **Shard-aware Batching** — `DeleteMany` and `LoadAndDeleteMany` pre-sort keys to process entire groups under a single lock acquisition per shard.
- **Atomic Compute** — Perform complex read-modify-write logic atomically within a single shard lock.
- **Go 1.24+** — Native `for range` iteration via `All()`.
- **Serialization** — Built-in `GobEncode` and `GobDecode` for easy persistence or network transfer.
- **Zero Dependencies** — Only the standard library.
- **`purego` Build Tag** — Builds without `unsafe` when needed.

## Installation

```bash
go get github.com/darxnet/goshard
```

### Building without `unsafe`

The default build uses `unsafe.Sizeof` (and nothing else from `unsafe`) to size the
batch buffers of `All`, `Range` and `LoadAndDeleteMany` for the value type, so they
stay on the stack for values of any size. If your project forbids `unsafe`, build
with the `purego` tag:

```bash
go build -tags purego ./...
```

With `purego` the buffers hold 256 entries and stay on the stack for entries
(key + value) up to 512 B; larger values cost one heap allocation per call.

## Quick Start

```go
// The zero value is ready to use!
var m goshard.Map[string, any]

// Comparable map with 64 shards
mc := goshard.NewComparableMap[int, int](64)

// Simple Load/Store
m.Store("rps", 1_000_000)
if v, ok := m.Load("rps"); ok {
    fmt.Printf("Current load: %d\n", v)
}

// Atomic update
counter, loaded := m.Compute("counter", func(key string, current int, loaded bool) (next int, keep bool) {
    return current + 1, true // keep = true to store
})

// Batch delete
m.DeleteMany([]string{"expired_1", "expired_2"})
```

## Benchmarks

> **Environment:** Apple M3 Pro · darwin/arm64 · Go 1.26
> `go test -bench=. -benchmem -count=3 -cpu=1,4,8 ./...`

### Parallel Store

| Implementation | 1 CPU | 4 CPUs | 8 CPUs | Allocs/op |
|---|---|---|---|---|
| **`goshard`** | 54 ns | **24 ns** | **20 ns** | **0** |
| `sync.Map` | 204 ns | 52 ns | 35 ns | 3 (65-75 B) |
| `sync.RWMutex` map | 37 ns | 182 ns | 163 ns | 0 |

`goshard` is slower than a bare mutex at 1 CPU (no contention), but **7.6x faster at 4 CPUs** where the global lock degrades under contention. `sync.Map` always allocates per-store regardless of CPU count.

### Write-Read-Delete Cycle

Sequential `Store` + `Load` + `Delete` per worker iteration; parallelism from `b.RunParallel` only.
This is the most representative high-churn workload: frequent writes and deletes with no idle keys.

| Implementation | 1 CPU | 4 CPUs | 8 CPUs | Allocs/op |
|---|---|---|---|---|
| **`goshard`** | 41 ns | **31 ns** | **29 ns** | **0** |
| `sync.Map` | 60 ns | 199 ns | 226 ns | 2-3 (63-74 B) |
| `sync.RWMutex` map | 26 ns | 153 ns | 176 ns | 0 |

`goshard` is **7.8x faster** than `sync.Map` and **6.1x faster** than a global mutex at 8 CPUs — with zero heap allocations. `sync.Map` degrades under write-heavy parallel load because its promotion mechanism serialises writers; the global mutex simply saturates.

### Batch Delete: `DeleteMany` vs Sequential Loop

Shard-sorted batching (`DeleteMany`) vs sequential single-key `Delete` calls.
`DeleteMany` deletes small batches (up to `max(8, shards/4)` keys) one by one, so it is never slower than the loop there; above that it sorts keys by shard and takes each shard lock once per group.

| Batch size (n) | Loop 1 CPU | `DeleteMany` 1 CPU | Loop 8 CPUs | `DeleteMany` 8 CPUs |
|---|---|---|---|---|
| 10 | 90 ns | 90 ns | 184 ns | 197 ns |
| 1,000 | 8.3 µs | 15.1 µs | 10.7 µs | **3.3 µs** |
| 10,000 | 83 µs | 188 µs | 105 µs | **33 µs** |
| 1,000,000 | 8.3 ms | 35.6 ms | 10.5 ms | **6.0 ms** |

At n=10,000 with 8 goroutines, `DeleteMany` is **3.2x faster** than a sequential loop. Single-threaded it is slower due to the slice sort; the batching benefit only materialises under parallel lock contention.

### Iteration and Batch Delete by Value Size

> `go test -bench=ValueSize -benchmem -count=3 ./...` (single goroutine)

`All` and `LoadAndDeleteMany` copy entries into a stack buffer sized for the value type, so they allocate nothing for values of any size.

| Value size | `All` (10,000 entries) | `LoadAndDeleteMany` (1,000 keys) | Allocs/op |
|---|---|---|---|
| 8 B | 77 µs | 16 µs | **0** |
| 48 B | 114 µs | 16 µs | **0** |
| 128 B | 193 µs | 18 µs | **0** |
| 1 KiB | 0.96 ms | 45 µs | **0** |
| 8 KiB | 10.2 ms | 308 µs | **0** |

With the `purego` tag the buffer is a fixed 256 entries; values above ~500 B then cost one heap allocation per call.

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.
