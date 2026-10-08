// Package goshard provides a high-performance, concurrent-safe sharded map.
//
// It is designed to minimize lock contention in high-throughput environments
// by distributing entries across multiple independent shards, each with its
// own RWMutex. This approach significantly outperforms a single sync.RWMutex
// for write-heavy workloads and scales better with the number of CPU cores.
package goshard

import (
	"bytes"
	"encoding/gob"
	"errors"
	"hash/maphash"
	"io"
	"iter"
	"math/bits"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
)

const (
	defaultShardFactor = 8
	minShardCount      = 64
)

type entry[K comparable, V any] struct {
	key   K
	value V
}

// cacheLinePad keeps every shard on its own cache lines so that writes to
// one shard do not slow down readers of its neighbours. cacheLineSize is set
// per architecture in the cacheline_*.go files.
type cacheLinePad struct{ _ [cacheLineSize]byte }

type shard[K comparable, V any] struct {
	_  cacheLinePad
	m  map[K]V
	rw sync.RWMutex
	_  cacheLinePad
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
	if n < 0 {
		panic("goshard: negative shard count")
	}

	sm := new(ComparableMap[K, V])
	sm.init(n)
	return sm
}

func (sm *Map[K, V]) idx(key K) uint64 {
	return maphash.Comparable(sm.seed, key) & sm.mask
}

func (sm *Map[K, V]) shard(key K) *shard[K, V] {
	sm.init(0)
	return &sm.shards[sm.idx(key)]
}

// fillSorted fills dst with one number per key: the shard index in the high
// 32 bits and the position in keys in the low 32 bits. Sorting dst then puts
// keys of the same shard next to each other.
// keys must hold fewer than 1<<32 elements and the map must be initialized.
func (sm *Map[K, V]) fillSorted(dst []uint64, keys []K) {
	if len(dst) != len(keys) {
		panic("dst and keys must have the same length")
	}

	for i, key := range keys {
		dst[i] = sm.idx(key)<<32 | uint64(i)
	}
	slices.Sort(dst)
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
	return func(yield func(key K, value V) bool) {
		sm.iterate(yield)
	}
}

func (sm *Map[K, V]) iterate(yield func(key K, value V) bool) {
	if sm.inited.Load() == 0 {
		return
	}

	sm.iterateBatched(yield)
}

// iterateBuf walks all shards. It copies up to len(buf) entries out of a
// shard, releases the shard lock and only then calls yield for them, so
// yield never runs under a lock.
func (sm *Map[K, V]) iterateBuf(buf []entry[K, V], yield func(key K, value V) bool) {
	for i := range sm.shards {
		s := &sm.shards[i]

		n := 0
		s.rw.RLock()
		for k, v := range s.m {
			buf[n] = entry[K, V]{key: k, value: v}
			n++
			if n == len(buf) {
				s.rw.RUnlock()
				for j := range n {
					if !yield(buf[j].key, buf[j].value) {
						return
					}
				}
				n = 0
				s.rw.RLock()
			}
		}
		s.rw.RUnlock()

		for j := range n {
			if !yield(buf[j].key, buf[j].value) {
				return
			}
		}
	}
}

// Range calls f sequentially for each key and value present in the map.
// If f returns false, range stops the iteration.
//
// This exists for compatibility with sync.Map; All should be preferred.
func (sm *Map[K, V]) Range(yield func(K, V) bool) {
	sm.iterate(yield)
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

// keyBatch is how many keys DeleteMany and LoadAndDeleteMany sort at once.
// The sort buffer takes 1024 * 8 B = 8 KiB of stack.
const keyBatch = 1 << 10

// fewKeys reports whether n keys should be deleted one by one instead of
// sorting them by shard first. Sorting helps only when several keys land in
// the same shard, so that one lock covers all of them. With more shards that
// happens less often, so the limit grows with the shard count.
// The numbers come from BenchmarkDeleteManyThreshold.
// The map must be initialized.
func (sm *Map[K, V]) fewKeys(n int) bool {
	return n <= max(8, len(sm.shards)/4)
}

// DeleteMany deletes each key in keys from the map.
func (sm *Map[K, V]) DeleteMany(keys []K) {
	if len(keys) == 0 || sm.inited.Load() == 0 {
		return
	}

	if sm.fewKeys(len(keys)) {
		for _, key := range keys {
			sm.Delete(key)
		}
		return
	}

	var buf [keyBatch]uint64

	for off := 0; off < len(keys); off += keyBatch {
		chunk := keys[off:min(off+keyBatch, len(keys))]
		batch := buf[:len(chunk)]
		sm.fillSorted(batch, chunk)

		for i := 0; i < len(batch); {
			shardID := batch[i] >> 32
			s := &sm.shards[shardID]
			s.rw.Lock()
			for ; i < len(batch) && batch[i]>>32 == shardID; i++ {
				delete(s.m, chunk[uint32(batch[i])]) //nolint:gosec // G115 low 32 bits hold the chunk index
			}
			s.rw.Unlock()
		}
	}
}

// LoadAndDeleteMany deletes each key in keys from the map and calls f with
// each key/value pair that was present.
// The function f is called after the key's shard lock is released.
func (sm *Map[K, V]) LoadAndDeleteMany(keys []K, f func(K, V)) {
	if len(keys) == 0 {
		return
	}

	if f == nil {
		panic("goshard: nil func")
	}

	if sm.inited.Load() == 0 {
		return
	}

	if sm.fewKeys(len(keys)) {
		for _, key := range keys {
			if value, ok := sm.LoadAndDelete(key); ok {
				f(key, value)
			}
		}
		return
	}

	sm.loadAndDeleteManyBatched(keys, f)
}

// loadAndDeleteManyBuf is LoadAndDeleteMany after the argument checks.
// Deleted entries are collected in removed while the shard is locked and
// handed to f after the lock is released; when removed fills up, the shard
// is processed in several rounds.
func (sm *Map[K, V]) loadAndDeleteManyBuf(keys []K, f func(K, V), removed []entry[K, V]) {
	var buf [keyBatch]uint64

	for off := 0; off < len(keys); off += keyBatch {
		chunk := keys[off:min(off+keyBatch, len(keys))]
		batch := buf[:len(chunk)]
		sm.fillSorted(batch, chunk)

		for i := 0; i < len(batch); {
			shardID := batch[i] >> 32
			s := &sm.shards[shardID]
			n := 0
			s.rw.Lock()
			for ; i < len(batch) && batch[i]>>32 == shardID && n < len(removed); i++ {
				key := chunk[uint32(batch[i])] //nolint:gosec // G115 low 32 bits hold the chunk index
				if value, ok := s.m[key]; ok {
					removed[n] = entry[K, V]{key: key, value: value}
					n++
					delete(s.m, key)
				}
			}
			s.rw.Unlock()

			for j := range n {
				f(removed[j].key, removed[j].value)
			}
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

	// Each encoded map is one shard of the source map, and its keys spread
	// over all shards of this map. Instead of building a temporary map per
	// shard, the keys are sorted by shard and written in groups under one
	// lock each.
	//
	// decoded, keys, values and order are reused for every encoded map.
	// gob adds entries to an existing map instead of making a new one, so
	// decoded is cleared before each Decode.
	var (
		decoded map[K]V
		keys    []K
		values  []V
		order   []uint64
	)

	for {
		clear(decoded)
		if err := dec.Decode(&decoded); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}

		keys, values = keys[:0], values[:0]
		for key, value := range decoded {
			keys = append(keys, key)
			values = append(values, value)
		}

		order = slices.Grow(order[:0], len(keys))[:len(keys)]
		sm.fillSorted(order, keys)

		for i := 0; i < len(order); {
			shardID := order[i] >> 32
			s := &sm.shards[shardID]
			s.rw.Lock()
			for ; i < len(order) && order[i]>>32 == shardID; i++ {
				j := uint32(order[i]) //nolint:gosec // G115 low 32 bits hold the key index
				s.m[keys[j]] = values[j]
			}
			s.rw.Unlock()
		}
	}

	return nil
}
