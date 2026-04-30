package goshard_test

import (
	"fmt"

	"github.com/darxnet/goshard"
)

// ExampleMap demonstrates basic store, load, and delete operations.
func ExampleMap() {
	m := goshard.NewMap[string, int](0)

	m.Store("active_users", 1024)

	if v, ok := m.Load("active_users"); ok {
		fmt.Println(v)
	}

	m.Delete("active_users")

	if _, ok := m.Load("active_users"); !ok {
		fmt.Println("gone")
	}

	// Output:
	// 1024
	// gone
}

// ExampleMap_zero shows that the zero value of Map is ready to use —
// no constructor call required.
func ExampleMap_zero() {
	var m goshard.Map[string, int]

	m.Store("score", 42)

	v, ok := m.Load("score")
	fmt.Println(v, ok)

	// Output:
	// 42 true
}

// ExampleMap_loadOrStore demonstrates get-or-set semantics: the first
// call stores the value, the second returns the existing one.
func ExampleMap_loadOrStore() {
	m := goshard.NewMap[string, int](0)

	actual, loaded := m.LoadOrStore("limit", 100)
	fmt.Println(actual, loaded) // stored

	actual, loaded = m.LoadOrStore("limit", 999)
	fmt.Println(actual, loaded) // existing value returned

	// Output:
	// 100 false
	// 100 true
}

// ExampleMap_compute demonstrates an atomic read-modify-write under
// the shard lock — safe to call concurrently.
func ExampleMap_compute() {
	m := goshard.NewMap[string, int](0)
	m.Store("counter", 0)

	m.Compute("counter", func(_ string, current int, loaded bool) (next int, keep bool) {
		return current + 1, true
	})

	v, _ := m.Load("counter")
	fmt.Println(v)

	// Output:
	// 1
}

// ExampleComparableMap_compareAndSwap demonstrates the lock-free-style
// compare-and-swap for comparable values.
func ExampleComparableMap_compareAndSwap() {
	m := goshard.NewComparableMap[string, string](0)
	m.Store("status", "idle")

	// Succeeds: current value matches "idle".
	fmt.Println(m.CompareAndSwap("status", "idle", "busy"))

	// Fails: current value is "busy", not "idle".
	fmt.Println(m.CompareAndSwap("status", "idle", "busy"))

	// Output:
	// true
	// false
}

// ExampleComparableMap_compareAndDelete demonstrates deleting a key only
// when its value matches the expected one.
func ExampleComparableMap_compareAndDelete() {
	m := goshard.NewComparableMap[string, int](0)
	m.Store("session", 42)

	fmt.Println(m.CompareAndDelete("session", 0))  // wrong value — no-op
	fmt.Println(m.CompareAndDelete("session", 42)) // matches — deleted

	_, ok := m.Load("session")
	fmt.Println(ok)

	// Output:
	// false
	// true
	// false
}

// ExampleMap_deleteMany demonstrates efficient batch deletion.
// Keys are sorted by shard internally to minimise lock acquisitions.
func ExampleMap_deleteMany() {
	m := goshard.NewMap[string, int](0)
	m.Store("s1", 1)
	m.Store("s2", 2)
	m.Store("s3", 3)

	m.DeleteMany([]string{"s1", "s2", "s3"})

	fmt.Println(m.Empty())

	// Output:
	// true
}

// ExampleMap_gobEncode demonstrates encoding a map to bytes and decoding
// it back into a new map using encoding/gob.
func ExampleMap_gobEncode() {
	src := goshard.NewMap[string, int](0)
	src.Store("a", 1)

	data, err := src.GobEncode()
	if err != nil {
		panic(err)
	}

	dst := goshard.NewMap[string, int](0)
	if err := dst.GobDecode(data); err != nil {
		panic(err)
	}

	v, ok := dst.Load("a")
	fmt.Println(v, ok)

	// Output:
	// 1 true
}
