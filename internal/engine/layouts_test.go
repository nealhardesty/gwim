package engine

import (
	"testing"

	"github.com/nealhardesty/gwim/internal/wm"
)

// reference screen — simple 1000x800 frame with origin at (0, 0). Most
// layout assertions become trivial integer math at this size.
var refScreen = wm.Rect{X: 0, Y: 0, W: 1000, H: 800}

func equalRect(a, b wm.Rect) bool {
	const eps = 1e-9
	return abs(a.X-b.X) < eps && abs(a.Y-b.Y) < eps && abs(a.W-b.W) < eps && abs(a.H-b.H) < eps
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// TestSnapLayouts is a table-driven exercise of every snap helper. It
// pins the math that downstream actions rely on, so a regression here is
// caught even if no UI is exercised.
func TestSnapLayouts(t *testing.T) {
	tests := []struct {
		name string
		fn   func(wm.Rect) wm.Rect
		want wm.Rect
	}{
		{"left-half", snapLeftHalf, wm.Rect{X: 0, Y: 0, W: 500, H: 800}},
		{"right-half", snapRightHalf, wm.Rect{X: 500, Y: 0, W: 500, H: 800}},
		{"top-half", snapTopHalf, wm.Rect{X: 0, Y: 0, W: 1000, H: 400}},
		{"bottom-half", snapBottomHalf, wm.Rect{X: 0, Y: 400, W: 1000, H: 400}},
		{"top-left", snapTopLeftQuarter, wm.Rect{X: 0, Y: 0, W: 500, H: 400}},
		{"top-right", snapTopRightQuarter, wm.Rect{X: 500, Y: 0, W: 500, H: 400}},
		{"bottom-left", snapBottomLeftQuarter, wm.Rect{X: 0, Y: 400, W: 500, H: 400}},
		{"bottom-right", snapBottomRightQuarter, wm.Rect{X: 500, Y: 400, W: 500, H: 400}},
		{"left-third", snapLeftThird, wm.Rect{X: 0, Y: 0, W: 1000.0 / 3, H: 800}},
		{"middle-third", snapMiddleThird, wm.Rect{X: 1000.0 / 3, Y: 0, W: 1000.0 / 3, H: 800}},
		{"right-third", snapRightThird, wm.Rect{X: 2 * 1000.0 / 3, Y: 0, W: 1000.0 / 3, H: 800}},
		{"maximize", snapMaximize, refScreen},
		// 1/4 height bottom strip, full width.
		{"strip-full", snapBottomStripFull, wm.Rect{X: 0, Y: 600, W: 1000, H: 200}},
		{"strip-left", snapBottomStripLeft, wm.Rect{X: 0, Y: 600, W: 500, H: 200}},
		{"strip-right", snapBottomStripRight, wm.Rect{X: 500, Y: 600, W: 500, H: 200}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.fn(refScreen)
			if !equalRect(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestSnapFourth covers the indexed fourth helper end to end including
// out-of-range clamping (defensive — the action layer never feeds bad
// indexes today, but if someone wires a typo we want graceful behaviour).
func TestSnapFourth(t *testing.T) {
	tests := []struct {
		idx  int
		want wm.Rect
	}{
		{0, wm.Rect{X: 0, Y: 0, W: 250, H: 800}},
		{1, wm.Rect{X: 250, Y: 0, W: 250, H: 800}},
		{2, wm.Rect{X: 500, Y: 0, W: 250, H: 800}},
		{3, wm.Rect{X: 750, Y: 0, W: 250, H: 800}},
		{-1, wm.Rect{X: 0, Y: 0, W: 250, H: 800}}, // clamped low
		{99, wm.Rect{X: 750, Y: 0, W: 250, H: 800}},
	}
	for _, tc := range tests {
		got := snapFourth(refScreen, tc.idx)
		if !equalRect(got, tc.want) {
			t.Errorf("idx=%d: got %+v, want %+v", tc.idx, got, tc.want)
		}
	}
}

// TestMoveBy verifies translation preserves dimensions.
func TestMoveBy(t *testing.T) {
	w := wm.Rect{X: 100, Y: 200, W: 300, H: 400}
	got := moveBy(w, 50, -25)
	want := wm.Rect{X: 150, Y: 175, W: 300, H: 400}
	if !equalRect(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

// TestResizeBy enforces both growth and the 50pt minimum-dimension clamp.
func TestResizeBy(t *testing.T) {
	w := wm.Rect{X: 100, Y: 200, W: 300, H: 400}

	grow := resizeBy(w, 100, 50)
	if grow.W != 400 || grow.H != 450 {
		t.Errorf("grow: got %+v", grow)
	}

	shrunk := resizeBy(w, -10000, -10000)
	if shrunk.W != 50 || shrunk.H != 50 {
		t.Errorf("clamp: got %+v", shrunk)
	}
}
