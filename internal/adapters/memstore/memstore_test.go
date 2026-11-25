package memstore

import (
	"testing"

	"github.com/federiconbaez/cortex9-simtest-harness/internal/domain"
)

// Contract test of the Storage port.
func TestStorageContract(t *testing.T) {
	var s domain.Storage = New()
	if _, ok := s.Get("k"); ok {
		t.Fatal("empty store returned a value")
	}
	s.Apply([]domain.KV{{Key: "k", Value: "1"}, {Key: "j", Value: "2"}})
	s.Apply([]domain.KV{{Key: "k", Value: "3"}})
	if v, _ := s.Get("k"); v != "3" {
		t.Fatalf("last write must win, got %q", v)
	}
	if v, ok := s.Get("j"); !ok || v != "2" {
		t.Fatal("batch lost a key")
	}
	if s.(*Store).Batches() != 2 {
		t.Fatal("batch count")
	}
}
