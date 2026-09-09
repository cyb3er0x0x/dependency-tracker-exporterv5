package collector

import "sync/atomic"

// Store holds the latest successful Snapshot. Reads are lock-free. A failed
// refresh never replaces a good Snapshot, so callers always see the most recent
// successful portfolio view.
type Store struct {
	v atomic.Pointer[Snapshot]
}

// NewStore returns an empty Store.
func NewStore() *Store { return &Store{} }

// Set atomically replaces the current Snapshot.
func (s *Store) Set(snap *Snapshot) { s.v.Store(snap) }

// Get returns the current Snapshot, or nil if no successful collection has
// completed yet.
func (s *Store) Get() *Snapshot { return s.v.Load() }
