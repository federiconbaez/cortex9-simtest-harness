package domain

import (
	"errors"
	"strings"
	"testing"
	"testing/quick"
)

var partitioned = FaultPlan{MinDelay: 1, MaxDelay: 9, ReorderExtra: 1, CrashEvery: 1, RestartMin: 1, RestartMax: 1,
	Partition: 400, PartitionEvery: 30, PartitionMin: 10, PartitionMax: 60}

func TestPartitionPlanParsing(t *testing.T) {
	p, err := ParseFaultPlan("partition=250")
	if err != nil {
		t.Fatal(err)
	}
	if p.Partition != 250 || p.PartitionEvery != defaultPartitionEvery ||
		p.PartitionMin != defaultPartitionMin || p.PartitionMax != defaultPartitionMax {
		t.Fatalf("defaults not applied: %+v", p)
	}
	if !strings.Contains(p.String(), ",partition=250,partition-every=100,partition-len=20-80") {
		t.Fatalf("canonical form: %s", p.String())
	}
	q, err := ParseFaultPlan(partitioned.String())
	if err != nil || q != partitioned {
		t.Fatalf("round trip: %+v %v", q, err)
	}
	// A disabled partition is canonical: its parameters are dropped.
	if p, err := ParseFaultPlan("partition=0,partition-every=7,partition-len=1-2"); err != nil || p != NoFaults() {
		t.Fatalf("disabled partition not normalized: %+v %v", p, err)
	}
	if strings.Contains(NoFaults().String(), "partition") {
		t.Fatal("disabled partition must not be printed")
	}
	for _, bad := range []string{"partition=1001", "partition=-1", "partition=5,partition-every=-1",
		"partition=5,partition-len=9-3", "partition=5,partition-len=0-3", "partition-len=x", "partition=5,partition-len=4"} {
		if _, err := ParseFaultPlan(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	stray := NoFaults()
	stray.PartitionEvery = 3
	if err := stray.Validate(); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("partition fields without partition: %v", err)
	}
}

// replayCut walks a trace and fails if any delivery crosses the active cut.
// It returns how many isolations and cross-node deliveries it saw.
func replayCut(t *testing.T, tr Trace) (isolations, delivered int) {
	t.Helper()
	var isolated NodeID
	for _, e := range tr {
		switch e.Kind {
		case KindIsolate:
			if isolated != "" {
				t.Fatalf("two partitions at once at t=%d", e.At)
			}
			isolated = e.Node
			isolations++
		case KindHeal:
			if e.Node != isolated {
				t.Fatalf("heal of %s while %s isolated", e.Node, isolated)
			}
			isolated = ""
		case KindDeliver:
			from := NodeID(strings.TrimPrefix(strings.Fields(e.Detail)[1], "from="))
			if isolated != "" && (from == isolated) != (e.Node == isolated) {
				t.Fatalf("t=%d: %s received from %s across the cut isolating %s", e.At, e.Node, from, isolated)
			}
			delivered++
		}
	}
	return isolations, delivered
}

func TestNoDeliveryCrossesAnActivePartitionProperty(t *testing.T) {
	sawIsolation := false
	prop := func(seed uint64) bool {
		s, err := pingPongSim(seed, partitioned, 600)
		if err != nil {
			t.Fatal(err)
		}
		iso, delivered := replayCut(t, s.Trace())
		if iso > 0 {
			sawIsolation = true
			if s.Stats().Partitions != iso {
				t.Fatalf("stats partitions=%d, trace=%d", s.Stats().Partitions, iso)
			}
		}
		return delivered > 0 && s.PendingWithinHorizon() == 0
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
	if !sawIsolation {
		t.Fatal("no seed produced a partition: the property is vacuous")
	}
}

func TestPartitionDropsInFlightMessagesAndHeals(t *testing.T) {
	// Find a seed where something was lost to a partition and traffic
	// crossed the former cut again after the heal.
	for seed := uint64(1); seed < 200; seed++ {
		s, err := pingPongSim(seed, partitioned, 600)
		if err != nil {
			t.Fatal(err)
		}
		if s.Stats().LostToPartition == 0 {
			continue
		}
		healedAt := Time(-1)
		for _, e := range s.Trace() {
			if e.Kind == KindHeal {
				healedAt = e.At
			}
			if e.Kind == KindDeliver && healedAt >= 0 && e.At > healedAt {
				return
			}
		}
	}
	t.Fatal("no seed showed a loss to a partition followed by delivery after the heal")
}

func TestPartitionedRunsAreDeterministicProperty(t *testing.T) {
	prop := func(seed uint64) bool {
		a, errA := pingPongSim(seed, partitioned, 400)
		b, errB := pingPongSim(seed, partitioned, 400)
		return errA == nil && errB == nil && a.Trace().Digest() == b.Trace().Digest() && a.Stats() == b.Stats()
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func TestSingleNodeNeverPartitions(t *testing.T) {
	s, err := NewSim(SimConfig{Seed: 1, Horizon: 500, Plan: partitioned})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddNode("solo", func() Process { return &echo{} }, newMapStorage(), false); err != nil {
		t.Fatal(err)
	}
	if err := s.Run(); err != nil {
		t.Fatal(err)
	}
	if s.Stats().Partitions != 0 {
		t.Fatal("a single node cannot be partitioned")
	}
}

func TestTraceWithPartitionsRoundTrips(t *testing.T) {
	s, err := pingPongSim(7, partitioned, 400)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseTrace(s.Trace().Format())
	if err != nil {
		t.Fatal(err)
	}
	if back.Digest() != s.Trace().Digest() {
		t.Fatal("trace with isolate/heal does not round trip")
	}
}
