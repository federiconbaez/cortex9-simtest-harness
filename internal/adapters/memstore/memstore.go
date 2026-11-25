// Package memstore implements the domain Storage port in memory. The store
// outlives the simulated process, which models durable storage across crashes.
package memstore

import "github.com/federiconbaez/cortex9-simtest-harness/internal/domain"

// Store is an in-memory durable store.
type Store struct {
	data    map[string]string
	batches int
}

// New returns an empty store.
func New() *Store { return &Store{data: map[string]string{}} }

// Get implements domain.Storage.
func (s *Store) Get(key string) (string, bool) {
	v, ok := s.data[key]
	return v, ok
}

// Apply implements domain.Storage; the batch is applied atomically because
// the simulator is single-threaded.
func (s *Store) Apply(batch []domain.KV) {
	for _, kv := range batch {
		s.data[kv.Key] = kv.Value
	}
	s.batches++
}

// Batches reports how many batches were applied.
func (s *Store) Batches() int { return s.batches }

var _ domain.Storage = (*Store)(nil)
