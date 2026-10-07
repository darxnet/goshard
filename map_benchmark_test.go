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

// Values of different sizes. The batch buffers in All and LoadAndDeleteMany
// stay on the stack only while 256 entries fit into 128 KiB, that is while
// one entry is at most 512 B. With int keys value504 is the largest value
// that still fits; value512 is the first one that does not.
type (
	value8   int64
	value128 [16]int64
	value504 [63]int64
	value512 [64]int64
	value1k  [128]int64
)

func benchmarkValueSize[V any](b *testing.B, size string) {
	b.Helper()

	for _, n := range []int{10, 10_000} {
		b.Run(fmt.Sprintf("All/%s/n=%d", size, n), func(b *testing.B) {
			var m goshard.Map[int, V]
			var v V
			for i := range n {
				m.Store(i, v)
			}

			b.ReportAllocs()
			for b.Loop() {
				for range m.All() {
				}
			}
		})
	}

	for _, n := range []int{11, 1_000} {
		b.Run(fmt.Sprintf("LoadAndDeleteMany/%s/n=%d", size, n), func(b *testing.B) {
			m := goshard.NewMap[int, V](0)
			keys := make([]int, n)
			for i := range keys {
				keys[i] = i
			}

			b.ReportAllocs()
			for b.Loop() {
				m.LoadAndDeleteMany(keys, func(int, V) {})
			}
		})
	}
}

func BenchmarkValueSize(b *testing.B) {
	benchmarkValueSize[value8](b, "8B")
	benchmarkValueSize[value128](b, "128B")
	benchmarkValueSize[value504](b, "504B")
	benchmarkValueSize[value512](b, "512B")
	benchmarkValueSize[value1k](b, "1KiB")
}

// BenchmarkDeleteManyFreshGoroutine runs every call in a new goroutine, like
// a request handler would. A new goroutine starts with a small stack, so a
// large buffer inside DeleteMany would force the stack to grow on each call.
func BenchmarkDeleteManyFreshGoroutine(b *testing.B) {
	m := goshard.NewMap[int, int](0)
	keys := make([]int, 11)
	for i := range keys {
		keys[i] = i
	}

	b.ReportAllocs()
	var wg sync.WaitGroup
	for b.Loop() {
		wg.Go(func() { m.DeleteMany(keys) })
		wg.Wait()
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

func BenchmarkGobDecode(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		src := goshard.NewMap[int, int](0)
		for i := range n {
			src.Store(i, i)
		}
		data, err := src.GobEncode()
		if err != nil {
			b.Fatal(err)
		}

		b.Run(fmt.Sprintf("Empty/n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				dst := goshard.NewMap[int, int](0)
				if err := dst.GobDecode(data); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(fmt.Sprintf("NonEmpty/n=%d", n), func(b *testing.B) {
			dst := goshard.NewMap[int, int](0)
			if err := dst.GobDecode(data); err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			for b.Loop() {
				if err := dst.GobDecode(data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDeleteManyThreshold compares DeleteMany with a plain loop of
// Delete calls for key counts around the fewKeys limit, max(8, shards/4).
func BenchmarkDeleteManyThreshold(b *testing.B) {
	deleteLoop := func(m *goshard.Map[int, int], keys []int) {
		for _, k := range keys {
			m.Delete(k)
		}
	}
	deleteMany := func(m *goshard.Map[int, int], keys []int) {
		m.DeleteMany(keys)
	}

	for _, shards := range []int{1, 16, 128} {
		for _, n := range []int{4, 8, 16, 32, 64, 128} {
			b.Run(fmt.Sprintf("shards=%d/n=%d/Loop", shards, n), func(b *testing.B) {
				benchmarkStoreDelete(b, shards, n, deleteLoop)
			})
			b.Run(fmt.Sprintf("shards=%d/n=%d/DeleteMany", shards, n), func(b *testing.B) {
				benchmarkStoreDelete(b, shards, n, deleteMany)
			})
		}
	}
}

func benchmarkStoreDelete(b *testing.B, shards, n int, del func(*goshard.Map[int, int], []int)) {
	b.Helper()

	m := goshard.NewMap[int, int](shards)
	b.RunParallel(func(pb *testing.PB) {
		keys := rand.Perm(n)
		for pb.Next() {
			for _, k := range keys {
				m.Store(k, k)
			}
			del(m, keys)
		}
	})
}
