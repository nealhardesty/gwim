package altswitch

import (
	"reflect"
	"testing"
)

func TestStashPromoteInserts(t *testing.T) {
	s := NewStash(0)
	s.Promote(Key{PID: 1, CGID: 100})
	s.Promote(Key{PID: 2, CGID: 200})
	got := s.Snapshot()
	want := []Key{{PID: 2, CGID: 200}, {PID: 1, CGID: 100}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot=%v want=%v", got, want)
	}
}

func TestStashPromoteMovesToFront(t *testing.T) {
	s := NewStash(0)
	s.Promote(Key{PID: 1, CGID: 100})
	s.Promote(Key{PID: 2, CGID: 200})
	s.Promote(Key{PID: 3, CGID: 300})
	s.Promote(Key{PID: 1, CGID: 100})
	got := s.Snapshot()
	want := []Key{
		{PID: 1, CGID: 100},
		{PID: 3, CGID: 300},
		{PID: 2, CGID: 200},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot=%v want=%v", got, want)
	}
}

func TestStashCapacityEvicts(t *testing.T) {
	s := NewStash(2)
	s.Promote(Key{PID: 1, CGID: 100})
	s.Promote(Key{PID: 2, CGID: 200})
	s.Promote(Key{PID: 3, CGID: 300})
	got := s.Snapshot()
	want := []Key{{PID: 3, CGID: 300}, {PID: 2, CGID: 200}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot=%v want=%v", got, want)
	}
}

func TestStashOrderPinsCurrentAndPrefersStash(t *testing.T) {
	s := NewStash(0)
	// Build MRU history: 3, 2, 1 (3 is most recent)
	s.Promote(Key{PID: 1, CGID: 100})
	s.Promote(Key{PID: 2, CGID: 200})
	s.Promote(Key{PID: 3, CGID: 300})

	// Enumerate windows in arbitrary order. Pretend 4 has never been seen.
	keys := []Key{
		{PID: 1, CGID: 100},
		{PID: 4, CGID: 400},
		{PID: 2, CGID: 200},
		{PID: 3, CGID: 300},
	}

	// Currently focused window is PID 3 — it should pin to position 0,
	// then MRU-ordered (2, 1), then never-seen (4) at the bottom.
	perm := s.Order(keys, Key{PID: 3, CGID: 300})
	want := []int{3, 2, 0, 1}
	if !reflect.DeepEqual(perm, want) {
		t.Fatalf("perm=%v want=%v", perm, want)
	}
}

func TestStashOrderPinnedAbsentFallsThrough(t *testing.T) {
	s := NewStash(0)
	s.Promote(Key{PID: 1, CGID: 100})
	keys := []Key{{PID: 1, CGID: 100}, {PID: 2, CGID: 200}}
	// Pinned key not in keys — should be skipped without panicking.
	perm := s.Order(keys, Key{PID: 99, CGID: 999})
	want := []int{0, 1}
	if !reflect.DeepEqual(perm, want) {
		t.Fatalf("perm=%v want=%v", perm, want)
	}
}

func TestStashForget(t *testing.T) {
	s := NewStash(0)
	s.Promote(Key{PID: 1, CGID: 100})
	s.Promote(Key{PID: 2, CGID: 200})
	s.Forget(Key{PID: 1, CGID: 100})
	got := s.Snapshot()
	want := []Key{{PID: 2, CGID: 200}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot=%v want=%v", got, want)
	}
}
