package goshard

import (
	"slices"
	"sync"
)

// 65536 * 16 (shardIndex size) = 1 MiB on 64-bit platforms.
const shardIndexPoolMaxItems = 65536

// shardIndex holds the shard and index of a key.
type shardIndex struct {
	shard int
	index int
}

// shardIndexPool is a sync.Pool for shardIndex slices.
var shardIndexPool = sync.Pool{
	New: func() any {
		buf := make([]shardIndex, 0, shardIndexPoolMaxItems)
		return &buf
	},
}

// acquireShardIndexSlice acquires a shardIndex slice from the pool, growing it to the given size.
func acquireShardIndexSlice(size int) *[]shardIndex {
	p := shardIndexPool.Get().(*[]shardIndex)
	buf := (*p)[:0]
	buf = slices.Grow(buf, size)[:size]
	*p = buf
	return p
}

// releaseShardIndexSlice releases a shardIndex slice back to the pool, resetting its size.
func releaseShardIndexSlice(p *[]shardIndex) {
	if cap(*p) > shardIndexPoolMaxItems {
		return
	}
	*p = (*p)[:0]
	shardIndexPool.Put(p)
}
