package goshard_test

import (
	"testing"
	"time"

	"github.com/darxnet/goshard"
)

// panicGob panics when gob encodes it.
type panicGob struct{}

func (panicGob) GobEncode() ([]byte, error) { panic("GobEncode") }

func (*panicGob) GobDecode([]byte) error { return nil }

// mustPanic calls f and fails the test if it does not panic.
func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()

	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", name)
		}
	}()

	f()
}

// completes fails the test if f does not return in time: a shard left locked
// by a panic makes the next write to it block forever.
func completes(t *testing.T, name string, f func()) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		defer close(done)

		f()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s blocked: a shard is still locked after a panic in user code", name)
	}
}

// TestUserCodePanicUnlocks checks that a panic in user code - callbacks,
// comparators, == on values of a dynamic type that is not comparable, gob
// encoders - leaves no shard locked, so later writes to it do not hang.
func TestUserCodePanicUnlocks(t *testing.T) {
	t.Parallel()

	panicky := func(_, _ any) bool { panic("eq") }

	tests := map[string]func(m *goshard.ComparableMap[int, any]){
		"CompareAndSwap": func(m *goshard.ComparableMap[int, any]) {
			m.Map.CompareAndSwap(1, nil, nil, panicky)
		},
		"CompareAndDelete": func(m *goshard.ComparableMap[int, any]) {
			m.Map.CompareAndDelete(1, nil, panicky)
		},
		"ComparableCompareAndSwap": func(m *goshard.ComparableMap[int, any]) {
			m.CompareAndSwap(1, []byte("v"), nil)
		},
		"ComparableCompareAndDelete": func(m *goshard.ComparableMap[int, any]) {
			m.CompareAndDelete(1, []byte("v"))
		},
		"Compute": func(m *goshard.ComparableMap[int, any]) {
			m.Compute(1, func(int, any, bool) (any, bool) { panic("compute") })
		},
		"Range": func(m *goshard.ComparableMap[int, any]) {
			m.Range(func(int, any) bool { panic("yield") })
		},
		"LoadAndDeleteMany": func(m *goshard.ComparableMap[int, any]) {
			m.LoadAndDeleteMany([]int{1}, func(int, any) { panic("f") })
		},
		"LoadAndDeleteManyBatched": func(m *goshard.ComparableMap[int, any]) {
			keys := make([]int, 64)
			for i := range keys {
				keys[i] = i + 1
			}
			m.LoadAndDeleteMany(keys, func(int, any) { panic("f") })
		},
	}

	for name, f := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// one shard: every key shares the lock that must be released
			m := goshard.NewComparableMap[int, any](1)
			m.Store(1, []byte("v"))

			mustPanic(t, name, func() { f(m) })
			completes(t, "Store after "+name, func() { m.Store(1, 2) })

			if v, ok := m.Load(1); !ok || v != 2 {
				t.Fatalf("Load after %s = (%v, %v), want (2, true)", name, v, ok)
			}
		})
	}
}

// TestGobEncodePanicUnlocks checks that a panic in the GobEncode method of a
// value, called while its shard is read-locked, releases the shard.
func TestGobEncodePanicUnlocks(t *testing.T) {
	t.Parallel()

	m := goshard.NewMap[int, panicGob](1)
	m.Store(1, panicGob{})

	mustPanic(t, "GobEncode", func() { _, _ = m.GobEncode() })
	completes(t, "Store after GobEncode", func() { m.Store(2, panicGob{}) })

	if n := m.Len(); n != 2 {
		t.Fatalf("Len after GobEncode = %d, want 2", n)
	}
}
