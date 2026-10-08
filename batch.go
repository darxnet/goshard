//go:build !purego

package goshard

import "unsafe"

// Entry buffer sizes by entry size. Each tier takes at most 8 KiB of stack,
// so a buffer never leaves the stack and is cheap to zero on every call.
// Bigger buffers do not help: copying an entry costs more than taking the
// shard lock once per buffer.
//
// unsafe.Sizeof of a type parameter is not a constant, so the buffer cannot
// be one array sized from it. Instead each tier declares its own array and
// the switch picks one. The compiler builds this code separately for every
// value type, where the size is known, and drops the other branches.
const (
	tier16   = 256 // entries up to 16 B
	tier64   = 128 // entries up to 64 B
	tier128  = 64  // entries up to 128 B
	tier512  = 16  // entries up to 512 B
	tier4096 = 2   // entries up to 4 KiB
	tierMax  = 1   // larger entries; above 128 KiB the buffer moves to the heap
)

func (sm *Map[K, V]) iterateBatched(yield func(key K, value V) bool) {
	switch size := unsafe.Sizeof(entry[K, V]{}); {
	case size <= 16:
		var buf [tier16]entry[K, V]
		sm.iterateBuf(buf[:], yield)
	case size <= 64:
		var buf [tier64]entry[K, V]
		sm.iterateBuf(buf[:], yield)
	case size <= 128:
		var buf [tier128]entry[K, V]
		sm.iterateBuf(buf[:], yield)
	case size <= 512:
		var buf [tier512]entry[K, V]
		sm.iterateBuf(buf[:], yield)
	case size <= 4096:
		var buf [tier4096]entry[K, V]
		sm.iterateBuf(buf[:], yield)
	default:
		var buf [tierMax]entry[K, V]
		sm.iterateBuf(buf[:], yield)
	}
}

func (sm *Map[K, V]) loadAndDeleteManyBatched(keys []K, f func(K, V)) {
	switch size := unsafe.Sizeof(entry[K, V]{}); {
	case size <= 16:
		var buf [tier16]entry[K, V]
		sm.loadAndDeleteManyBuf(keys, f, buf[:])
	case size <= 64:
		var buf [tier64]entry[K, V]
		sm.loadAndDeleteManyBuf(keys, f, buf[:])
	case size <= 128:
		var buf [tier128]entry[K, V]
		sm.loadAndDeleteManyBuf(keys, f, buf[:])
	case size <= 512:
		var buf [tier512]entry[K, V]
		sm.loadAndDeleteManyBuf(keys, f, buf[:])
	case size <= 4096:
		var buf [tier4096]entry[K, V]
		sm.loadAndDeleteManyBuf(keys, f, buf[:])
	default:
		var buf [tierMax]entry[K, V]
		sm.loadAndDeleteManyBuf(keys, f, buf[:])
	}
}
