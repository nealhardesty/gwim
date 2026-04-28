//go:build darwin

package macos

import (
	"testing"

	"github.com/nealhardesty/gwim/internal/wm"
)

// TestScaleRectBetweenScreens verifies that translating a window from one
// screen to another preserves its relative geometry. This is pure math,
// independent of cgo, so it can run anywhere darwin can build.
func TestScaleRectBetweenScreens(t *testing.T) {
	src := wm.Rect{X: 0, Y: 0, W: 1000, H: 800}
	dst := wm.Rect{X: 1000, Y: 0, W: 2000, H: 1200}

	tests := []struct {
		name string
		win  wm.Rect
		want wm.Rect
	}{
		{
			"top-left quarter",
			wm.Rect{X: 0, Y: 0, W: 500, H: 400},
			wm.Rect{X: 1000, Y: 0, W: 1000, H: 600},
		},
		{
			"bottom-right quarter",
			wm.Rect{X: 500, Y: 400, W: 500, H: 400},
			wm.Rect{X: 2000, Y: 600, W: 1000, H: 600},
		},
		{
			"full-screen",
			wm.Rect{X: 0, Y: 0, W: 1000, H: 800},
			wm.Rect{X: 1000, Y: 0, W: 2000, H: 1200},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := scaleRectBetweenScreens(tc.win, src, dst)
			if !rectEq(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestOverlapArea covers the helper used by screenIndexForRect's fallback path.
func TestOverlapArea(t *testing.T) {
	a := wm.Rect{X: 0, Y: 0, W: 100, H: 100}
	b := wm.Rect{X: 50, Y: 50, W: 100, H: 100}
	if got := overlapArea(a, b); got != 2500 {
		t.Errorf("overlap got %v want 2500", got)
	}
	disjoint := wm.Rect{X: 200, Y: 200, W: 50, H: 50}
	if got := overlapArea(a, disjoint); got != 0 {
		t.Errorf("disjoint overlap got %v want 0", got)
	}
}

func rectEq(a, b wm.Rect) bool {
	const eps = 1e-9
	d := func(x, y float64) float64 {
		if x > y {
			return x - y
		}
		return y - x
	}
	return d(a.X, b.X) < eps && d(a.Y, b.Y) < eps && d(a.W, b.W) < eps && d(a.H, b.H) < eps
}
