//go:build purego

package goshard

// iterBatch is the entry buffer size when the package is built without
// unsafe. The buffer is a fixed-size array, and Go keeps such arrays on the
// stack only up to 128 KiB: with 256 entries that holds for entries (key +
// value) up to 512 B. Bigger entries move the buffer to the heap, which costs
// one allocation per call. The default build picks the size per value type
// instead, see batch.go.
const iterBatch = 256

func (sm *Map[K, V]) iterateBatched(yield func(key K, value V) bool) {
	var buf [iterBatch]entry[K, V]
	sm.iterateBuf(buf[:], yield)
}

func (sm *Map[K, V]) loadAndDeleteManyBatched(keys []K, f func(K, V)) {
	var buf [iterBatch]entry[K, V]
	sm.loadAndDeleteManyBuf(keys, f, buf[:])
}
