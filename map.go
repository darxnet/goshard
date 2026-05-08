// Package goshard provides a high-performance, concurrent-safe sharded map.
//
// It is designed to minimize lock contention in high-throughput environments
// by distributing entries across multiple independent shards, each with its
// own RWMutex. This approach significantly outperforms a single sync.RWMutex
// for write-heavy workloads and scales better with the number of CPU cores.
package goshard

import (
	"bytes"
	"cmp"
	"encoding/gob"
	"errors"
	"hash/maphash"
	"io"
	"iter"
	"maps"
	"math/bits"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/cpu"
)

const (
	defaultShardFactor = 8
	minShardCount      = 64
)

type entry[K comparable, V any] struct {
	key   K
	value V
}

type shardIndex struct {
	shard uint64
	index int
}

type shard[K comparable, V any] struct {
	_  cpu.CacheLinePad
	m  map[K]V
	rw sync.RWMutex
	_  cpu.CacheLinePad
}

// Map is a concurrent-safe sharded map optimized for reduced lock contention
// compared to a Go map paired with a [sync.RWMutex].
//
// The zero Map is empty and ready for use. A Map must not be copied after first use.
type Map[K comparable, V any] struct {
	inited atomic.Uint32
	initMu sync.Mutex

	shards []shard[K, V]
	mask   uint64
	seed   maphash.Seed
}

// ComparableMap is a Map with specialized atomic operations for comparable values.
type ComparableMap[K comparable, V comparable] struct {
	Map[K, V]
}

func nextPow2(x int) uint64 {
	if x <= 1 {
		return 1
	}

	return 1 << bits.Len(uint(x-1))
}

func (sm *Map[K, V]) init(n int) {
	if sm.inited.Load() == 0 {
		sm.initSlow(n)
	}
}

func (sm *Map[K, V]) initSlow(n int) {
	sm.initMu.Lock()
	defer sm.initMu.Unlock()

	if sm.inited.Load() != 0 {
		return
	}

	if n == 0 {
		n = max(minShardCount, runtime.GOMAXPROCS(0)*defaultShardFactor)
	}

	pow2 := nextPow2(n)

	sm.shards = make([]shard[K, V], pow2)
	for i := range sm.shards {
		sm.shards[i].m = make(map[K]V, 1)
	}
	sm.mask = pow2 - 1
	sm.seed = maphash.MakeSeed()

	sm.inited.Store(1)
}

// NewMap returns a sharded map with n shards.
// If n is zero, NewMap chooses a concurrency-oriented default;
// otherwise n is rounded up to the next power of two.
func NewMap[K comparable, V any](n int) *Map[K, V] {
	if n < 0 {
		panic("goshard: negative shard count")
	}

	sm := new(Map[K, V])
	sm.init(n)
	return sm
}

// NewComparableMap returns a sharded map for comparable values.
// If n is zero, NewComparableMap chooses a concurrency-oriented default;
// otherwise n is rounded up to the next power of two.
func NewComparableMap[K comparable, V comparable](n int) *ComparableMap[K, V] {
	return &ComparableMap[K, V]{Map: *NewMap[K, V](n)}
}

func (sm *Map[K, V]) idx(key K) uint64 {
	return maphash.Comparable(sm.seed, key) & sm.mask
}

func (sm *Map[K, V]) shard(key K) *shard[K, V] {
	sm.init(0)
	return &sm.shards[sm.idx(key)]
}

func (sm *Map[K, V]) fillSorted(dst []shardIndex, keys []K) {
	sm.init(0)
	if len(dst) != len(keys) {
		panic("dst and keys must have the same length")
	}

	for i, key := range keys {
		dst[i] = shardIndex{
			index: i,
			shard: sm.idx(key),
		}
	}

	slices.SortFunc(dst, func(a, b shardIndex) int {
		return cmp.Compare(a.shard, b.shard)
	})
}

// Load returns the value stored in the map for a key, or the zero value if no
// value is present.
// The ok result indicates whether value was found in the map.
func (sm *Map[K, V]) Load(key K) (value V, ok bool) {
	s := sm.shard(key)
	s.rw.RLock()
	value, ok = s.m[key]
	s.rw.RUnlock()
	return value, ok
}

// LoadOrStore returns the existing value for the key if present.
// Otherwise, it stores and returns the given value.
// The loaded result is true if the value was loaded, false if stored.
func (sm *Map[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	s := sm.shard(key)
	s.rw.Lock()
	if actual, loaded = s.m[key]; !loaded {
		s.m[key] = value
		actual = value
	}
	s.rw.Unlock()
	return actual, loaded
}

// Store sets the value for a key.
func (sm *Map[K, V]) Store(key K, value V) {
	s := sm.shard(key)
	s.rw.Lock()
	s.m[key] = value
	s.rw.Unlock()
}

// Swap swaps the value for a key and returns the previous value if any.
// The loaded result reports whether the key was present.
func (sm *Map[K, V]) Swap(key K, value V) (previous V, loaded bool) {
	s := sm.shard(key)
	s.rw.Lock()
	previous, loaded = s.m[key]
	s.m[key] = value
	s.rw.Unlock()
	return previous, loaded
}

// CompareAndSwap swaps the old and new values for a key
// if the value stored in the map is equal to old
// according to the eq function.
// The eq function is called while the shard for the key is locked.
func (sm *Map[K, V]) CompareAndSwap(key K, old, new V, eq func(current, old V) bool) (swapped bool) {
	if eq == nil {
		panic("goshard: nil comparator")
	}

	s := sm.shard(key)
	s.rw.Lock()
	defer s.rw.Unlock()

	if current, loaded := s.m[key]; loaded && eq(current, old) {
		s.m[key] = new
		swapped = true
	}
	return swapped
}

// CompareAndSwap swaps the old and new values for key
// if the value stored in the map is equal to old.
func (sm *ComparableMap[K, V]) CompareAndSwap(key K, old, new V) (swapped bool) {
	s := sm.shard(key)
	s.rw.Lock()
	defer s.rw.Unlock()

	if current, loaded := s.m[key]; loaded && current == old {
		s.m[key] = new
		swapped = true
	}
	return swapped
}

// LoadAndDelete deletes the value for a key, returning the previous value if any.
// The loaded result reports whether the key was present.
func (sm *Map[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	s := sm.shard(key)
	s.rw.Lock()
	value, loaded = s.m[key]
	if loaded {
		delete(s.m, key)
	}
	s.rw.Unlock()
	return value, loaded
}

// Delete deletes the value for a key.
func (sm *Map[K, V]) Delete(key K) {
	s := sm.shard(key)
	s.rw.Lock()
	delete(s.m, key)
	s.rw.Unlock()
}

// CompareAndDelete deletes the entry for key if its value is equal to old
// according to the eq function.
// The eq function is called while the shard for the key is locked.
func (sm *Map[K, V]) CompareAndDelete(key K, old V, eq func(current, old V) bool) (deleted bool) {
	if eq == nil {
		panic("goshard: nil comparator")
	}

	s := sm.shard(key)
	s.rw.Lock()
	defer s.rw.Unlock()

	if current, loaded := s.m[key]; loaded && eq(current, old) {
		delete(s.m, key)
		deleted = true
	}

	return deleted
}

// CompareAndDelete deletes the entry for key if its value is equal to old.
func (sm *ComparableMap[K, V]) CompareAndDelete(key K, old V) (deleted bool) {
	s := sm.shard(key)
	s.rw.Lock()
	defer s.rw.Unlock()

	if current, loaded := s.m[key]; loaded && current == old {
		delete(s.m, key)
		deleted = true
	}
	return deleted
}

// All returns an iterator over each key and value present in the map.
//
// The iterator does not necessarily correspond to any consistent snapshot of the
// Map's contents: no key will be visited more than once, but if the value
// for any key is stored or deleted concurrently (including by yield), the iterator
// may reflect any mapping for that key from any point during iteration. The iterator
// does not block other methods on the receiver; even yield itself may call any
// method on the Map.
func (sm *Map[K, V]) All() iter.Seq2[K, V] {
	sm.init(0)
	return func(yield func(key K, value V) bool) {
		// batchSize is a common batch size for dynamic value sizes
		const batchSize = 1 << 10

		// stack-allocated buffer
		var buf [batchSize]entry[K, V]
		bufLen := 0

		for i := range sm.shards {
			s := &sm.shards[i]

			s.rw.RLock()
			for k, v := range s.m {
				buf[bufLen] = entry[K, V]{key: k, value: v} //nolint:gosec // G602 slice index not out of range
				bufLen++
				if bufLen == batchSize {
					s.rw.RUnlock()
					for j := range bufLen {
						if !yield(buf[j].key, buf[j].value) {
							return
						}
					}
					bufLen = 0
					s.rw.RLock()
				}
			}
			s.rw.RUnlock()

			for j := range bufLen {
				if !yield(buf[j].key, buf[j].value) {
					return
				}
			}
			bufLen = 0
		}
	}
}

// Range calls f sequentially for each key and value present in the map.
// If f returns false, range stops the iteration.
//
// This exists for compatibility with sync.Map; All should be preferred.
func (sm *Map[K, V]) Range(yield func(K, V) bool) {
	sm.All()(yield)
}

// Clear deletes all entries from the map.
func (sm *Map[K, V]) Clear() {
	if sm.inited.Load() == 0 {
		return
	}

	for i := range sm.shards {
		s := &sm.shards[i]
		s.rw.Lock()
		clear(s.m)
		s.rw.Unlock()
	}
}

// Len returns the number of elements in the map.
func (sm *Map[K, V]) Len() int {
	if sm.inited.Load() == 0 {
		return 0
	}

	n := 0
	for i := range sm.shards {
		s := &sm.shards[i]
		s.rw.RLock()
		n += len(s.m)
		s.rw.RUnlock()
	}
	return n
}

// Empty returns true if the map contains no elements.
func (sm *Map[K, V]) Empty() bool {
	if sm.inited.Load() == 0 {
		return true
	}

	for i := range sm.shards {
		s := &sm.shards[i]
		s.rw.RLock()
		empty := len(s.m) == 0
		s.rw.RUnlock()
		if !empty {
			return false
		}
	}
	return true
}

// DeleteMany deletes each key in keys from the map.
func (sm *Map[K, V]) DeleteMany(keys []K) {
	// 8192 * 16 ([shardIndex] size) = 128 KiB on 64-bit platforms.
	// Exceeding this value may trigger a heap allocation.
	const batchSize = 1 << 13

	if len(keys) == 0 {
		return
	}

	if len(keys) <= 10 {
		for _, key := range keys {
			sm.Delete(key)
		}
		return
	}

	var buf [batchSize]shardIndex

	for chunk := range slices.Chunk(keys, batchSize) {
		batch := buf[:len(chunk)]
		sm.fillSorted(batch, chunk)

		for i := 0; i < len(batch); {
			j := i + 1
			shardID := batch[i].shard
			for j < len(batch) && batch[j].shard == shardID {
				j++
			}

			s := &sm.shards[shardID]
			s.rw.Lock()
			for k := i; k < j; k++ {
				delete(s.m, chunk[batch[k].index])
			}
			s.rw.Unlock()

			i = j
		}
	}
}

// LoadAndDeleteMany deletes each key in keys from the map and calls f with
// each key/value pair that was present.
// The function f is called after the key's shard lock is released.
func (sm *Map[K, V]) LoadAndDeleteMany(keys []K, f func(K, V)) {
	// 2048 * 16 ([shardIndex] size) = 32 KiB on 64-bit platforms.
	// 2048 * sizeof(entry[K,V]) = 128 KiB for entries up to 64 B -
	// safe for all primitives and most struct value types.
	const batchSize = 1 << 11

	if len(keys) == 0 {
		return
	}

	if f == nil {
		panic("goshard: nil func")
	}

	if len(keys) <= 10 {
		for _, key := range keys {
			if value, ok := sm.LoadAndDelete(key); ok {
				f(key, value)
			}
		}
		return
	}

	var buf [batchSize]shardIndex
	var removedBuf [batchSize]entry[K, V]

	for chunk := range slices.Chunk(keys, batchSize) {
		batch := buf[:len(chunk)]
		sm.fillSorted(batch, chunk)

		for i := 0; i < len(batch); {
			j := i + 1
			shardID := batch[i].shard
			for j < len(batch) && batch[j].shard == shardID {
				j++
			}

			s := &sm.shards[shardID]
			s.rw.Lock()
			removedLen := 0
			for k := i; k < j; k++ {
				key := chunk[batch[k].index]
				if value, ok := s.m[key]; ok {
					removedBuf[removedLen] = entry[K, V]{key: key, value: value}
					removedLen++
					delete(s.m, key)
				}
			}
			s.rw.Unlock()

			for k := range removedLen {
				f(removedBuf[k].key, removedBuf[k].value)
			}

			i = j
		}
	}
}

// Compute replaces or deletes the value for a key using f.
// If f returns keep=false, the key is deleted.
// The function f is called while the key's shard is locked.
func (sm *Map[K, V]) Compute(key K, f func(key K, current V, loaded bool) (next V, keep bool)) (value V, loaded bool) {
	if f == nil {
		panic("goshard: nil compute function")
	}

	s := sm.shard(key)

	s.rw.Lock()
	defer s.rw.Unlock()

	current, loaded := s.m[key]
	next, keep := f(key, current, loaded)
	if keep {
		s.m[key] = next
		value = next
	} else {
		delete(s.m, key)
	}

	return value, loaded
}

// GobEncode encodes all non-empty map shards as a sequence of gob maps.
// Empty shards are skipped; GobDecode reads until EOF and handles a variable
// number of encoded maps correctly.
func (sm *Map[K, V]) GobEncode() ([]byte, error) {
	if sm.inited.Load() == 0 {
		return nil, nil
	}

	var w bytes.Buffer
	enc := gob.NewEncoder(&w)

	for i := range sm.shards {
		s := &sm.shards[i]
		s.rw.RLock()
		if len(s.m) == 0 {
			s.rw.RUnlock()
			continue
		}
		err := enc.Encode(s.m)
		s.rw.RUnlock()
		if err != nil {
			return nil, err
		}
	}

	return w.Bytes(), nil
}

// GobDecode merges gob-encoded shard maps into the map. It applies each decoded
// gob map immediately, so malformed input can leave earlier decoded entries
// merged before GobDecode returns an error.
func (sm *Map[K, V]) GobDecode(bs []byte) error {
	if len(bs) == 0 {
		return nil
	}

	sm.init(0)
	dec := gob.NewDecoder(bytes.NewReader(bs))

	groups := make([]map[K]V, len(sm.shards))
	counts := make([]int, len(sm.shards))

	for {
		var decoded map[K]V

		if err := dec.Decode(&decoded); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}

		clear(groups)
		clear(counts)

		// Count keys per destination shard to pre-size intermediate maps,
		// avoiding incremental growth during the second pass.
		for key := range decoded {
			counts[sm.idx(key)]++
		}

		for idx, count := range counts {
			if count != 0 {
				groups[idx] = make(map[K]V, count)
			}
		}

		for key, value := range decoded {
			idx := sm.idx(key)
			groups[idx][key] = value
		}

		for idx, group := range groups {
			if group == nil {
				continue
			}

			s := &sm.shards[idx]
			s.rw.Lock()
			if len(s.m) == 0 {
				s.m = group
			} else {
				maps.Copy(s.m, group)
			}
			s.rw.Unlock()
		}
	}

	return nil
}
