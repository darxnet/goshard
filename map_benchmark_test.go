package goshard_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/darxnet/goshard"
)

var counts = []int{10, 1_000, 10_000, 1_000_000}

func prepareKeys(b *testing.B) []int {
	b.Helper()

	keys := make([]int, max(b.N, 1_000_000))
	for i := range keys {
		keys[i] = i
	}

	r := rand.New(rand.NewSource(42))
	r.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })

	return keys
}

// BenchmarkWriteReadDeleteCycle measures the combined cost of Store + Load +
// Delete in a single goroutine iteration. Operations are sequential within
// each worker; parallelism comes solely from b.RunParallel.
// This avoids the goroutine-scheduling and closure-allocation noise that
// nested `go func()` launches would introduce into the measurement.
func BenchmarkWriteReadDeleteCycle(b *testing.B) {
	keys := prepareKeys(b)

	b.Run("goshardMap", func(b *testing.B) {
		var m goshard.Map[int, int]
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := keys[i%len(keys)]
				i++
				m.Store(key, key)
				_, _ = m.Load(key)
				m.Delete(key)
			}
		})
	})

	b.Run("syncMap", func(b *testing.B) {
		var m sync.Map
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := keys[i%len(keys)]
				i++
				m.Store(key, key)
				_, _ = m.Load(key)
				m.Delete(key)
			}
		})
	})

	b.Run("mutexMap", func(b *testing.B) {
		m := make(map[int]int)
		var rw sync.RWMutex
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := keys[i%len(keys)]
				i++
				rw.Lock()
				m[key] = key
				rw.Unlock()
				rw.RLock()
				_ = m[key]
				rw.RUnlock()
				rw.Lock()
				delete(m, key)
				rw.Unlock()
			}
		})
	})
}

func BenchmarkStoreParallel(b *testing.B) {
	keys := prepareKeys(b)

	b.Run("goshardMap", func(b *testing.B) {
		m := goshard.NewMap[int, int](0)

		b.RunParallel(func(pb *testing.PB) {
			i := 0

			for pb.Next() {
				idx := i % len(keys)
				key := keys[idx]
				i++

				m.Store(key, key)
			}
		})
	})

	b.Run("syncMap", func(b *testing.B) {
		var m sync.Map

		b.RunParallel(func(pb *testing.PB) {
			i := 0

			for pb.Next() {
				idx := i % len(keys)
				key := keys[idx]
				i++

				m.Store(key, key)
			}
		})
	})

	b.Run("mutexMap", func(b *testing.B) {
		var m = make(map[int]int)
		var rw = sync.RWMutex{}

		b.RunParallel(func(pb *testing.PB) {
			i := 0

			for pb.Next() {
				idx := i % len(keys)
				key := keys[idx]
				i++

				rw.Lock()
				m[key] = key
				rw.Unlock()
			}
		})
	})
}

func BenchmarkDeleteManyParallel(b *testing.B) {
	keys := prepareKeys(b)

	for _, count := range counts {
		b.Run(fmt.Sprintf("Loop/n=%d", count), func(b *testing.B) {
			m := goshard.NewMap[int, int](0)
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					for _, k := range keys[:count] {
						m.Delete(k)
					}
				}
			})
		})

		b.Run(fmt.Sprintf("DeleteMany/n=%d", count), func(b *testing.B) {
			m := goshard.NewMap[int, int](0)
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					m.DeleteMany(keys[:count])
				}
			})
		})
	}
}

func BenchmarkRangeParallel(b *testing.B) {
	for _, count := range counts {
		b.Run(fmt.Sprintf("goshardMap/n=%d", count), func(b *testing.B) {
			var m goshard.Map[int, int]
			for i := range count {
				m.Store(i, i)
			}
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0

				for pb.Next() {
					i++
					if i%2 == 0 {
						m.Store(i, i)
					} else {
						m.Range(func(k, v int) bool { return k == v })
					}
				}
			})
		})

		b.Run(fmt.Sprintf("syncMap/n=%d", count), func(b *testing.B) {
			var m sync.Map
			for i := range count {
				m.Store(i, i)
			}
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0

				for pb.Next() {
					i++
					if i%2 == 0 {
						m.Store(i, i)
					} else {
						m.Range(func(k, v any) bool { return k == v })
					}
				}
			})
		})
	}
}
