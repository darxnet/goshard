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
- **Go 1.23+ Ready** — Native support for `for range` iterators via `All()`.
- **Serialization** — Built-in `GobEncode` and `GobDecode` for easy persistence or network transfer.

## Installation

```bash
go get github.com/darxnet/goshard
```

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
| **`goshard`** | 65 ns | **26 ns** | **21 ns** | **0** |
| `sync.Map` | 234 ns | 59 ns | 38 ns | 3 (64 B) |
| `sync.RWMutex` map | 43 ns | 215 ns | 172 ns | 0 |

`goshard` is slower than a bare mutex at 1 CPU (no contention), but **8.2x faster at 4 CPUs** where the global lock degrades under contention. `sync.Map` always allocates per-store regardless of CPU count.

### Write-Read-Delete Cycle

Sequential `Store` + `Load` + `Delete` per worker iteration; parallelism from `b.RunParallel` only.
This is the most representative high-churn workload: frequent writes and deletes with no idle keys.

| Implementation | 1 CPU | 4 CPUs | 8 CPUs | Allocs/op |
|---|---|---|---|---|
| **`goshard`** | 42 ns | **31 ns** | **30 ns** | **0** |
| `sync.Map` | 63 ns | 207 ns | 221 ns | 2-3 (63-74 B) |
| `sync.RWMutex` map | 25 ns | 159 ns | 174 ns | 0 |

`goshard` is **7.4x faster** than `sync.Map` and **5.8x faster** than a global mutex at 8 CPUs — with zero heap allocations. `sync.Map` degrades under write-heavy parallel load because its promotion mechanism serialises writers; the global mutex simply saturates.

### Batch Delete: `DeleteMany` vs Sequential Loop

Shard-sorted batching (`DeleteMany`) vs sequential single-key `Delete` calls.
Breakeven is around n=1,000 under parallel load; below that the sort overhead is net-negative single-threaded.

| Batch size (n) | Loop 1 CPU | `DeleteMany` 1 CPU | Loop 8 CPUs | `DeleteMany` 8 CPUs |
|---|---|---|---|---|
| 10 | 113 ns | 113 ns | 209 ns | 222 ns |
| 1,000 | 10,403 ns | 25,428 ns | 14,614 ns | **5,551 ns** |
| 10,000 | 104,279 ns | 280,367 ns | 140,003 ns | **43,180 ns** |
| 1,000,000 | 10.5 ms | 41.7 ms | 14.0 ms | **6.4 ms** |

At n=10,000 with 8 goroutines, `DeleteMany` is **3.2x faster** than a sequential loop. Single-threaded it is slower due to the slice sort; the batching benefit only materialises under parallel lock contention.

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.
