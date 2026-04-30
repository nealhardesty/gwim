package altswitch

import (
	"sort"

	"github.com/nealhardesty/gwim/internal/wm"
)

// GroupOrder returns a permutation of indices into items that sorts them
// by [wm.WindowInfo.GroupRank] ascending, preserving the original order
// within each group (stable secondary).
//
// The platform enumerator is responsible for encoding the desired
// section order in GroupRank — on macOS, the focused-window's Space sits
// at rank 0, then current Spaces per display, then non-current Spaces,
// with sticky windows at the tail. Other platforms typically leave
// GroupRank at 0 for every entry, in which case GroupOrder collapses to
// an identity permutation.
//
// The function is pure so it stays easily unit-testable; the macOS
// switcher invokes it on the MRU-ordered slice immediately before
// passing the result to the native overlay.
func GroupOrder(items []wm.WindowInfo) []int {
	n := len(items)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return items[idx[a]].GroupRank < items[idx[b]].GroupRank
	})
	return idx
}
