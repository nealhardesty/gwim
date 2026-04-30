package altswitch

import (
	"reflect"
	"testing"

	"github.com/nealhardesty/gwim/internal/wm"
)

// TestGroupOrderStable verifies that within a single group (everyone
// has the same GroupRank), GroupOrder returns the identity permutation
// — preserving the MRU order produced by Stash.Order.
func TestGroupOrderStable(t *testing.T) {
	items := []wm.WindowInfo{
		{PID: 1, CGID: 100, GroupRank: 5},
		{PID: 2, CGID: 200, GroupRank: 5},
		{PID: 3, CGID: 300, GroupRank: 5},
	}
	got := GroupOrder(items)
	want := []int{0, 1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

// TestGroupOrderFocusedSpaceFirst exercises the typical macOS scenario:
// the focused window's Space gets rank 0, others get larger ranks, and
// sticky windows sort to the tail. MRU order within each group must be
// preserved.
func TestGroupOrderFocusedSpaceFirst(t *testing.T) {
	// Slot 0: focused window pinned by MRU (rank 0).
	// Slot 1: another window on a different Space (rank 1001).
	// Slot 2: another focused-Space window (rank 0).
	// Slot 3: sticky (max rank).
	// Slot 4: still on different Space (rank 1001).
	items := []wm.WindowInfo{
		{PID: 1, CGID: 100, GroupRank: 0},
		{PID: 2, CGID: 200, GroupRank: 1001},
		{PID: 3, CGID: 300, GroupRank: 0},
		{PID: 4, CGID: 400, GroupRank: 1<<31 - 1},
		{PID: 5, CGID: 500, GroupRank: 1001},
	}
	got := GroupOrder(items)
	// Expected: [0, 2] (focused-space group, in MRU order),
	//           [1, 4] (next group, in MRU order),
	//           [3]    (sticky, last).
	want := []int{0, 2, 1, 4, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

// TestGroupOrderEmpty makes sure the helper handles a zero-length slice
// without panicking — the switcher already short-circuits on empty
// enumerations but defensive correctness matters here.
func TestGroupOrderEmpty(t *testing.T) {
	got := GroupOrder(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty perm, got=%v", got)
	}
}

// TestGroupOrderIdentityWhenAllZero exercises the non-macOS code path
// where every WindowInfo carries the zero-value GroupRank. The result
// must be a no-op identity permutation so the Windows / future-platform
// callers see no behaviour change.
func TestGroupOrderIdentityWhenAllZero(t *testing.T) {
	items := []wm.WindowInfo{
		{PID: 1, CGID: 100},
		{PID: 2, CGID: 200},
		{PID: 3, CGID: 300},
		{PID: 4, CGID: 400},
	}
	got := GroupOrder(items)
	want := []int{0, 1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}
