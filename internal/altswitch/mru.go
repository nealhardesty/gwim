// Package altswitch holds the platform-agnostic helpers behind the
// Alt-Tab window switcher described in ALTTAB.md.
//
// The package's only state is the Most-Recently-Used (MRU) stash, which
// orders enumerated windows so the switcher overlay matches familiar
// Alt-Tab behaviour: most recently focused first, current window pinned
// at index 0. The actual overlay UI, key event tap, and AX-driven window
// raising live in internal/platform/<os>/altswitch.
package altswitch

import "sync"

// Key uniquely identifies a window across enumerations.
//
// On macOS, CGID is the CGWindowID returned by _AXUIElementGetWindow for
// the window's AXUIElementRef. PID is included so legitimately distinct
// windows on different processes never collide (e.g. helper processes
// that reuse low CGWindowIDs across their own restarts).
type Key struct {
	PID  int32
	CGID uint32
}

// Stash is a thread-safe MRU list of window keys.
//
// Promote moves a key to position 0, evicting older entries past the
// configured capacity. Order takes a freshly enumerated slice of keys and
// returns the permutation matching MRU ordering.
type Stash struct {
	mu    sync.Mutex
	order []Key
	cap   int
}

// NewStash creates a stash bounded to capacity entries (LRU evicts at the
// tail). Pass 0 or negative for an unbounded stash.
func NewStash(capacity int) *Stash {
	return &Stash{cap: capacity}
}

// Promote moves k to position 0, inserting if not already present.
// Evicts trailing entries past the configured capacity.
func (s *Stash) Promote(k Key) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.order {
		if e == k {
			copy(s.order[1:i+1], s.order[0:i])
			s.order[0] = k
			return
		}
	}
	s.order = append([]Key{k}, s.order...)
	if s.cap > 0 && len(s.order) > s.cap {
		s.order = s.order[:s.cap]
	}
}

// Forget drops k from the stash if present. Used when a window dies.
func (s *Stash) Forget(k Key) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.order {
		if e == k {
			s.order = append(s.order[:i], s.order[i+1:]...)
			return
		}
	}
}

// Order returns a permutation of indices into keys that places them in
// MRU order. The pinned key (if non-zero and present in keys) always
// occupies index 0 of the result; remaining keys appear next in stash
// order, then any keys not yet in the stash in their original order.
func (s *Stash) Order(keys []Key, pinned Key) []int {
	s.mu.Lock()
	stashOrder := make([]Key, len(s.order))
	copy(stashOrder, s.order)
	s.mu.Unlock()

	used := make([]bool, len(keys))
	out := make([]int, 0, len(keys))

	if pinned != (Key{}) {
		for i, k := range keys {
			if k == pinned {
				out = append(out, i)
				used[i] = true
				break
			}
		}
	}

	for _, sk := range stashOrder {
		if sk == pinned {
			continue
		}
		for i, k := range keys {
			if used[i] {
				continue
			}
			if k == sk {
				out = append(out, i)
				used[i] = true
				break
			}
		}
	}

	for i := range keys {
		if !used[i] {
			out = append(out, i)
		}
	}
	return out
}

// Snapshot returns a copy of the current MRU order. Useful for tests and
// debugging only — production code should call Order or Promote instead.
func (s *Stash) Snapshot() []Key {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Key, len(s.order))
	copy(out, s.order)
	return out
}
