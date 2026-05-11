package goshard

import "testing"

func TestInitSlowAlreadyInitialized(t *testing.T) {
	t.Parallel()

	var m Map[int, int]
	m.initSlow(1)
	m.initSlow(10)

	if len(m.shards) != 1 {
		t.Fatalf("shard count changed after second initSlow: got %d", len(m.shards))
	}
}

func TestFillSortedPanicsOnMismatchedLengths(t *testing.T) {
	t.Parallel()

	m := NewMap[int, int](1)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for mismatched fillSorted inputs")
		}
	}()

	m.fillSorted(make([]shardIndex, 1), []int{1, 2})
}
