package goshard_test

import (
	"testing"

	"github.com/darxnet/goshard"
)

// Values of different sizes. With int keys an entry is 8 B larger than the
// value. The default build picks the entry buffer size from the entry size
// (see batch.go), so these types cover every tier: 16 B, 64 B, 128 B, 512 B,
// 4 KiB and above. With the purego tag the buffer holds 256 entries and
// stays on the stack only for entries up to 512 B: value504 is the largest
// value that still fits, value512 the first one that does not.
type (
	value8   int64
	value48  [6]int64
	value120 [15]int64
	value128 [16]int64
	value504 [63]int64
	value512 [64]int64
	value1k  [128]int64
	value8k  [1024]int64
)

// testValueSize fills a single shard with more entries than any buffer
// tier holds, so All and LoadAndDeleteMany have to refill their buffers.
func testValueSize[V any](t *testing.T) {
	t.Helper()

	const n = 1100

	m := goshard.NewMap[int, V](1)
	keys := make([]int, n)
	for i := range keys {
		keys[i] = i
		var v V
		m.Store(i, v)
	}

	seen := make(map[int]bool, n)
	for k := range m.All() {
		if seen[k] {
			t.Fatalf("key %d yielded twice", k)
		}
		seen[k] = true
	}
	if len(seen) != n {
		t.Fatalf("All yielded %d keys, want %d", len(seen), n)
	}

	clear(seen)
	m.LoadAndDeleteMany(keys, func(k int, _ V) {
		if seen[k] {
			t.Fatalf("key %d reported twice", k)
		}
		seen[k] = true
	})
	if len(seen) != n {
		t.Fatalf("LoadAndDeleteMany reported %d keys, want %d", len(seen), n)
	}
	if !m.Empty() {
		t.Fatalf("map should be empty, got %d entries", m.Len())
	}
}

func TestValueSizes(t *testing.T) {
	t.Parallel()

	t.Run("8B", func(t *testing.T) { t.Parallel(); testValueSize[value8](t) })
	t.Run("48B", func(t *testing.T) { t.Parallel(); testValueSize[value48](t) })
	t.Run("120B", func(t *testing.T) { t.Parallel(); testValueSize[value120](t) })
	t.Run("128B", func(t *testing.T) { t.Parallel(); testValueSize[value128](t) })
	t.Run("504B", func(t *testing.T) { t.Parallel(); testValueSize[value504](t) })
	t.Run("512B", func(t *testing.T) { t.Parallel(); testValueSize[value512](t) })
	t.Run("1KiB", func(t *testing.T) { t.Parallel(); testValueSize[value1k](t) })
	t.Run("8KiB", func(t *testing.T) { t.Parallel(); testValueSize[value8k](t) })
}
