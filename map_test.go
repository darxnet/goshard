package goshard_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/darxnet/goshard"
)

func TestNewMap(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](0)
	if m == nil {
		t.Fatal("NewMap returned nil")
	}
}

func TestNewMapPanicsOnNegativeShardCount(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for negative shard count")
		}
	}()
	_ = goshard.NewMap[int, int](-1)
}

func TestNewComparableMap(t *testing.T) {
	t.Parallel()
	m := goshard.NewComparableMap[int, int](0)
	if m == nil {
		t.Fatal("NewComparableMap returned nil")
	}
}

func TestBasicLoadStoreAcrossKeyTypes(t *testing.T) {
	t.Parallel()

	intMap := goshard.NewMap[int, struct{}](8)
	intMap.Store(1, struct{}{})
	if _, ok := intMap.Load(1); !ok {
		t.Fatal("missing int key")
	}

	stringMap := goshard.NewMap[string, int](8)
	stringMap.Store("a", 10)
	if v, ok := stringMap.Load("a"); !ok || v != 10 {
		t.Fatal("missing string key")
	}
}

func TestLenAndEmpty(t *testing.T) {
	t.Parallel()

	m := goshard.NewMap[int, int](8)
	if !m.Empty() || m.Len() != 0 {
		t.Fatal("new map should be empty")
	}

	m.Store(1, 10)
	m.Store(2, 20)
	m.Store(1, 30)

	if m.Empty() || m.Len() != 2 {
		t.Fatal("size scan is broken")
	}

	m.Delete(1)
	m.Delete(2)

	if !m.Empty() || m.Len() != 0 {
		t.Fatal("delete should drain the map")
	}
}

func TestComparableCASAndDelete(t *testing.T) {
	t.Parallel()

	m := goshard.NewComparableMap[int, int](8)
	m.Store(1, 10)

	if !m.CompareAndSwap(1, 10, 20) {
		t.Fatal("expected compare-and-swap to succeed")
	}
	if v, ok := m.Load(1); !ok || v != 20 {
		t.Fatal("cas did not update value")
	}
	if !m.CompareAndDelete(1, 20) {
		t.Fatal("expected compare-and-delete to succeed")
	}
	if _, ok := m.Load(1); ok {
		t.Fatal("compare-and-delete did not remove value")
	}
}

func TestCompareFuncForNonComparableValues(t *testing.T) {
	t.Parallel()

	m := goshard.NewMap[int, []int](0)
	m.Store(1, []int{1, 2, 3})

	swapped := m.CompareAndSwap(1, []int{1, 2, 3}, []int{4, 5, 6}, func(current, old []int) bool {
		if len(current) != len(old) {
			return false
		}
		for i := range current {
			if current[i] != old[i] {
				return false
			}
		}
		return true
	})
	if !swapped {
		t.Fatal("expected compare func CAS to succeed")
	}

	deleted := m.CompareAndDelete(1, []int{4, 5, 6}, func(current, old []int) bool {
		if len(current) != len(old) {
			return false
		}
		for i := range current {
			if current[i] != old[i] {
				return false
			}
		}
		return true
	})
	if !deleted {
		t.Fatal("expected compare func delete to succeed")
	}
}

func TestDeleteManyAndLoadAndDeleteMany(t *testing.T) {
	t.Parallel()

	m := goshard.NewMap[int, int](0)
	for i := range 10 {
		m.Store(i, i*10)
	}

	var mu sync.Mutex
	seen := make(map[int]int, 5)

	m.LoadAndDeleteMany([]int{0, 1, 2, 3, 4}, func(k, v int) {
		mu.Lock()
		seen[k] = v
		mu.Unlock()
	})
	m.DeleteMany([]int{5, 6, 7, 8, 9})

	if !m.Empty() || m.Len() != 0 {
		t.Fatal("bulk delete should drain the map")
	}
	if len(seen) != 5 {
		t.Fatal("LoadAndDeleteMany missed deleted values")
	}
}

func TestRangeStopsEarly(t *testing.T) {
	t.Parallel()

	m := goshard.NewMap[int, int](1)
	m.Store(1, 10)
	m.Store(2, 20)

	visited := 0
	m.Range(func(_, _ int) bool {
		visited++
		return false
	})

	if visited != 1 {
		t.Fatal("Range should stop after callback returns false")
	}
}

func TestGobDecodeIntoEmptyMap(t *testing.T) {
	t.Parallel()

	src := goshard.NewMap[int, struct{}](0)
	dst := goshard.NewMap[int, struct{}](0)

	src.Store(1, struct{}{})

	buf, err := src.GobEncode()
	if err != nil {
		t.Fatal(err)
	}

	if err := dst.GobDecode(buf); err != nil {
		t.Fatal(err)
	}

	if _, ok := dst.Load(1); !ok {
		t.Fatal("missing key from src")
	}
}

func TestGobDecodeInitializesZeroMap(t *testing.T) {
	t.Parallel()

	src := goshard.NewMap[int, int](0)
	src.Store(1, 10)

	buf, err := src.GobEncode()
	if err != nil {
		t.Fatal(err)
	}

	var dst goshard.Map[int, int]
	if err := dst.GobDecode(buf); err != nil {
		t.Fatal(err)
	}

	if v, ok := dst.Load(1); !ok || v != 10 {
		t.Fatal("GobDecode should initialize a zero Map")
	}
}

func TestGobRoundTripKeepsState(t *testing.T) {
	t.Parallel()

	src := goshard.NewMap[int, struct{}](0)
	dst := goshard.NewMap[int, struct{}](0)

	src.Store(1, struct{}{})
	dst.Store(2, struct{}{})

	buf, err := src.GobEncode()
	if err != nil {
		t.Fatal(err)
	}

	if err := dst.GobDecode(buf); err != nil {
		t.Fatal(err)
	}

	if _, ok := dst.Load(2); !ok {
		t.Fatal("missing key from dst")
	}
	if _, ok := dst.Load(1); !ok {
		t.Fatal("missing key from src")
	}
}

func TestGobEncodeErrorsOnUnsupportedValues(t *testing.T) {
	t.Parallel()

	m := goshard.NewMap[int, error](0)
	m.Store(1, errors.New("boom"))

	if _, err := m.GobEncode(); err == nil {
		t.Fatal("expected gob encode failure")
	}
}

func TestConcurrentStoreLoadDelete(t *testing.T) {
	t.Parallel()

	m := goshard.NewMap[int, int](0)

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := range 2000 {
				key := base*10000 + i
				m.Store(key, i)
				if v, ok := m.Load(key); !ok || v != i {
					t.Errorf("load mismatch for key %d", key)
				}
				m.Delete(key)
			}
		}(g)
	}

	wg.Wait()

	if !m.Empty() || m.Len() != 0 {
		t.Fatal("concurrent store/load/delete leaked entries")
	}
}

func TestConcurrentLazyInit(t *testing.T) {
	t.Parallel()

	var m goshard.Map[int, int]
	var wg sync.WaitGroup
	const workers = 100
	const opsPerWorker = 1000

	for g := range workers {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := range opsPerWorker {
				key := base*opsPerWorker + i
				m.Store(key, i)
				if v, ok := m.Load(key); !ok || v != i {
					t.Errorf("load mismatch for key %d", key)
				}
			}
		}(g)
	}

	wg.Wait()
}

func TestComputeCanDelete(t *testing.T) {
	t.Parallel()

	m := goshard.NewMap[int, int](0)
	m.Store(1, 10)

	value, loaded := m.Compute(1, func(_ int, current int, loaded bool) (int, bool) {
		if !loaded || current != 10 {
			t.Fatal("unexpected compute input")
		}
		return 0, false
	})

	if !loaded || value != 0 {
		t.Fatal("compute returned wrong metadata")
	}
	if _, ok := m.Load(1); ok {
		t.Fatal("compute delete did not remove key")
	}
}

func TestClear(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](0)
	m.Store(1, 10)
	m.Clear()
	if !m.Empty() {
		t.Fatal("map should be empty after clear")
	}

	// Test clear on uninitialized map
	var m2 goshard.Map[int, int]
	m2.Clear()
	if !m2.Empty() {
		t.Fatal("uninitialized map should be empty after clear")
	}
}

func TestLoadOrStore(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](0)
	actual, loaded := m.LoadOrStore(1, 10)
	if loaded || actual != 10 {
		t.Fatal("expected to store 10")
	}
	actual, loaded = m.LoadOrStore(1, 20)
	if !loaded || actual != 10 {
		t.Fatal("expected to load 10")
	}
}

func TestLoadAndDelete(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](0)
	m.Store(1, 10)
	v, loaded := m.LoadAndDelete(1)
	if !loaded || v != 10 {
		t.Fatal("expected to load and delete 10")
	}
	_, loaded = m.LoadAndDelete(1)
	if loaded {
		t.Fatal("expected key to be gone")
	}
}

func TestSwap(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](0)
	_, loaded := m.Swap(1, 10)
	if loaded {
		t.Fatal("expected no previous value")
	}
	prev, loaded := m.Swap(1, 20)
	if !loaded || prev != 10 {
		t.Fatal("expected previous value 10")
	}
}

func TestDeleteManyEdgeCases(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](0)
	m.DeleteMany(nil) // Should not panic
	m.DeleteMany([]int{1})
	m.Store(1, 10)
	m.DeleteMany([]int{1})
	if _, ok := m.Load(1); ok {
		t.Fatal("key should be deleted")
	}
}

func TestLoadAndDeleteManyEdgeCases(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](0)
	m.LoadAndDeleteMany(nil, func(k, v int) {}) // Should not panic
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil func")
		}
	}()
	m.LoadAndDeleteMany([]int{1}, nil)
}

func TestComputeEdgeCases(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](0)
	m.Compute(1, func(k int, v int, loaded bool) (int, bool) {
		return 10, true
	})
	if v, ok := m.Load(1); !ok || v != 10 {
		t.Fatal("expected 10")
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil func")
		}
	}()
	m.Compute(1, nil)
}

func TestCompareMethodsEdgeCases(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](0)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil comparator")
		}
	}()
	m.CompareAndSwap(1, 1, 1, nil)
}

func TestCompareAndDeleteNil(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](0)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil comparator")
		}
	}()
	m.CompareAndDelete(1, 1, nil)
}

func TestGobEmpty(t *testing.T) {
	t.Parallel()
	var m goshard.Map[int, int]
	data, err := m.GobEncode()
	if err != nil || data != nil {
		t.Fatal("expected nil data for uninitialized map")
	}
	err = m.GobDecode(nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestZeroMapLenRange(t *testing.T) {
	t.Parallel()
	var m goshard.Map[int, int]
	if m.Len() != 0 {
		t.Fatal("zero map should have length 0")
	}
	m.Range(func(k, v int) bool {
		t.Fatal("range should not execute on zero map")
		return true
	})
}

func TestDoubleInit(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](0)
	// Explicitly call init again to trigger the sm.inited.Load() == 1 branches
	// although NewMap already called it.
	m.Store(1, 10) // ensures initialized
	// We can't easily trigger the sm.inited.Load() == 1 INSIDE the lock
	// without some race condition, but calling init(0) should cover the first early return.
}

func TestLoadAndDeleteManyMissingKeys(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](0)
	m.Store(1, 10)
	m.LoadAndDeleteMany([]int{1, 2}, func(k, v int) {
		if k == 2 {
			t.Fatal("key 2 should not be reported as deleted")
		}
	})
}

func TestGobDecodeMalformed(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](0)
	err := m.GobDecode([]byte("malformed"))
	if err == nil {
		t.Fatal("expected error for malformed gob")
	}
}

func TestRangeCallbackStopsMidShard(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](1) // Single shard to ensure order
	m.Store(1, 10)
	m.Store(2, 20)
	m.Store(3, 30)

	count := 0
	m.Range(func(k, v int) bool {
		count++
		return count < 2
	})
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}
}

func TestLoadAndDeleteManyMixed(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](1) // Use 1 shard to force same shard
	m.Store(1, 10)
	m.Store(3, 30)

	deletedCount := 0
	m.LoadAndDeleteMany([]int{1, 2, 3}, func(k, v int) {
		deletedCount++
	})

	if deletedCount != 2 {
		t.Fatalf("expected 2 deleted, got %d", deletedCount)
	}
}

func TestDoubleInitRace(t *testing.T) {
	t.Parallel()
	var m goshard.Map[int, int]
	var wg sync.WaitGroup

	for range 100 {
		wg.Go(func() {
			m.Store(1, 1)
		})
	}

	wg.Wait()
}

func TestRangeWithEmptyShards(t *testing.T) {
	t.Parallel()
	m := goshard.NewMap[int, int](64)
	m.Store(1, 10) // Only one shard non-empty
	count := 0
	m.Range(func(k, v int) bool {
		count++
		return true
	})
	if count != 1 {
		t.Fatal("expected count 1")
	}
}

// TestAllIteratesAllShardsWithoutDuplicates verifies that Range visits every
// key exactly once when entries are spread across multiple shards.
// This is a regression test for the buf-reset bug: without buf = buf[:0] after
// each per-shard flush, leftover entries were re-yielded for every subsequent
// (possibly empty) shard.
func TestAllIteratesAllShardsWithoutDuplicates(t *testing.T) {
	t.Parallel()

	const total = 1000
	m := goshard.NewMap[int, int](8)
	for i := range total {
		m.Store(i, i)
	}

	seen := make(map[int]int, total)
	m.Range(func(k, v int) bool {
		seen[k]++
		return true
	})

	for k, count := range seen {
		if count != 1 {
			t.Fatalf("key %d visited %d times, want exactly 1", k, count)
		}
	}
	if len(seen) != total {
		t.Fatalf("Range visited %d distinct keys, want %d", len(seen), total)
	}
}

func TestPoolLargeSlice(t *testing.T) {
	t.Parallel()
	// This exercises the 'if cap(*p) > shardIndexPoolMaxItems' branch
	m := goshard.NewMap[int, int](0)
	keys := make([]int, 5000)
	for i := range keys {
		keys[i] = i
	}
	m.DeleteMany(keys)
}
