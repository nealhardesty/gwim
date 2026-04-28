// Package engine implements GWiM's platform-agnostic business logic:
// layout calculations, action dispatch, the suspension middleware, and
// the shortcut definition table.
//
// The engine talks only to the wm.* interfaces, so it has no compile-time
// dependency on macOS or Windows code. Concrete platform implementations
// are injected by main.go.
package engine

import "github.com/nealhardesty/gwim/internal/wm"

// Layout calculations.
//
// Every helper takes the screen's visible frame and returns a target
// window rect. Inputs and outputs are in AX coordinates (top-left origin)
// because that's what the wm.Window interface expects.
//
// All sizing math is intentionally float-based to avoid pixel drift when
// dividing by three, four, etc. The wm layer rounds when handing the
// rect to the OS.

// snapLeftHalf returns the left half of the screen.
func snapLeftHalf(s wm.Rect) wm.Rect {
	return wm.Rect{X: s.X, Y: s.Y, W: s.W / 2, H: s.H}
}

// snapRightHalf returns the right half of the screen.
func snapRightHalf(s wm.Rect) wm.Rect {
	return wm.Rect{X: s.X + s.W/2, Y: s.Y, W: s.W / 2, H: s.H}
}

// snapTopHalf returns the top half of the screen.
func snapTopHalf(s wm.Rect) wm.Rect {
	return wm.Rect{X: s.X, Y: s.Y, W: s.W, H: s.H / 2}
}

// snapBottomHalf returns the bottom half of the screen.
func snapBottomHalf(s wm.Rect) wm.Rect {
	return wm.Rect{X: s.X, Y: s.Y + s.H/2, W: s.W, H: s.H / 2}
}

// Quarters: top-left, top-right, bottom-left, bottom-right.

func snapTopLeftQuarter(s wm.Rect) wm.Rect {
	return wm.Rect{X: s.X, Y: s.Y, W: s.W / 2, H: s.H / 2}
}

func snapTopRightQuarter(s wm.Rect) wm.Rect {
	return wm.Rect{X: s.X + s.W/2, Y: s.Y, W: s.W / 2, H: s.H / 2}
}

func snapBottomLeftQuarter(s wm.Rect) wm.Rect {
	return wm.Rect{X: s.X, Y: s.Y + s.H/2, W: s.W / 2, H: s.H / 2}
}

func snapBottomRightQuarter(s wm.Rect) wm.Rect {
	return wm.Rect{X: s.X + s.W/2, Y: s.Y + s.H/2, W: s.W / 2, H: s.H / 2}
}

// Thirds (full-height columns).

func snapLeftThird(s wm.Rect) wm.Rect {
	return wm.Rect{X: s.X, Y: s.Y, W: s.W / 3, H: s.H}
}

func snapMiddleThird(s wm.Rect) wm.Rect {
	return wm.Rect{X: s.X + s.W/3, Y: s.Y, W: s.W / 3, H: s.H}
}

func snapRightThird(s wm.Rect) wm.Rect {
	return wm.Rect{X: s.X + 2*s.W/3, Y: s.Y, W: s.W / 3, H: s.H}
}

// Fourths (full-height columns), indexed left to right (0..3).

func snapFourth(s wm.Rect, index int) wm.Rect {
	if index < 0 {
		index = 0
	}
	if index > 3 {
		index = 3
	}
	q := s.W / 4
	return wm.Rect{X: s.X + float64(index)*q, Y: s.Y, W: q, H: s.H}
}

// Bottom horizontal strips at 1/4 the screen height (per design 3.3 §m,n,,).

func snapBottomStripFull(s wm.Rect) wm.Rect {
	h := s.H / 4
	return wm.Rect{X: s.X, Y: s.Y + s.H - h, W: s.W, H: h}
}

func snapBottomStripLeft(s wm.Rect) wm.Rect {
	h := s.H / 4
	return wm.Rect{X: s.X, Y: s.Y + s.H - h, W: s.W / 2, H: h}
}

func snapBottomStripRight(s wm.Rect) wm.Rect {
	h := s.H / 4
	return wm.Rect{X: s.X + s.W/2, Y: s.Y + s.H - h, W: s.W / 2, H: h}
}

// snapMaximize fills the entire visible frame (frame-based, NOT native fullscreen).
func snapMaximize(s wm.Rect) wm.Rect {
	return s
}

// Movement / resize step in points. Matches the legacy Hammerspoon script.
const moveStep = 100.0

// moveBy returns the window rect translated by (dx, dy) without changing size.
func moveBy(w wm.Rect, dx, dy float64) wm.Rect {
	return wm.Rect{X: w.X + dx, Y: w.Y + dy, W: w.W, H: w.H}
}

// resizeBy returns the window rect with width/height adjusted by (dw, dh).
// Width/height are clamped to a minimum of 50pt to avoid windows that
// can't be grabbed afterwards.
func resizeBy(w wm.Rect, dw, dh float64) wm.Rect {
	const minDim = 50.0
	nw := w.W + dw
	if nw < minDim {
		nw = minDim
	}
	nh := w.H + dh
	if nh < minDim {
		nh = minDim
	}
	return wm.Rect{X: w.X, Y: w.Y, W: nw, H: nh}
}
