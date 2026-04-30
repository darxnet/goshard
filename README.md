# goshard

[![Go Reference](https://pkg.go.dev/badge/github.com/darxnet/goshard.svg)](https://pkg.go.dev/github.com/darxnet/goshard)
[![Go](https://github.com/darxnet/goshard/actions/workflows/go.yml/badge.svg)](https://github.com/darxnet/goshard/actions/workflows/go.yml)
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
var mc goshard.MapComparable[string, int]

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

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.
