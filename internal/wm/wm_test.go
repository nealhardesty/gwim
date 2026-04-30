package wm

import "testing"

// TestWindowInfoMinimizedHiddenFields makes sure the optional Minimized
// and Hidden flags survive a struct literal round-trip. They drive the
// dim treatment in the Alt-Tab overlay; if a future refactor ever drops
// or renames them we want a compile-time / test failure here rather than
// a silent loss of the dim signal in the UI.
func TestWindowInfoMinimizedHiddenFields(t *testing.T) {
	cases := []struct {
		name string
		info WindowInfo
		min  bool
		hid  bool
	}{
		{
			name: "default",
			info: WindowInfo{PID: 1, CGID: 100, Title: "t", AppName: "a"},
			min:  false,
			hid:  false,
		},
		{
			name: "minimized",
			info: WindowInfo{PID: 2, CGID: 200, Title: "t", AppName: "a", Minimized: true},
			min:  true,
			hid:  false,
		},
		{
			name: "hidden",
			info: WindowInfo{PID: 3, CGID: 300, Title: "t", AppName: "a", Hidden: true},
			min:  false,
			hid:  true,
		},
		{
			name: "both",
			info: WindowInfo{PID: 4, CGID: 400, Title: "t", AppName: "a", Minimized: true, Hidden: true},
			min:  true,
			hid:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.info.Minimized != tc.min {
				t.Errorf("Minimized=%v want=%v", tc.info.Minimized, tc.min)
			}
			if tc.info.Hidden != tc.hid {
				t.Errorf("Hidden=%v want=%v", tc.info.Hidden, tc.hid)
			}
		})
	}
}
