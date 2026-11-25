package app_test

import (
	"testing"
	"testing/quick"

	"github.com/federiconbaez/cortex9-simtest-harness/internal/adapters/kv"
	"github.com/federiconbaez/cortex9-simtest-harness/internal/adapters/memstore"
	"github.com/federiconbaez/cortex9-simtest-harness/internal/app"
	"github.com/federiconbaez/cortex9-simtest-harness/internal/domain"
)

func kvConfig(v kv.Variant) app.Config {
	return app.Config{SUT: kv.New(v, 3), NewStorage: func() domain.Storage { return memstore.New() }}
}

// Planted bug: kv-lazy acknowledges puts before they are durable. The
// harness must find it; numbers measured over seeds 1..200 are pinned so a
// change in the simulator that hides the bug is caught (regression).
func TestPlantedBugIsFound(t *testing.T) {
	ex, err := app.Explore(kvConfig(kv.LazyFlush), 1, 200)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Failures == 0 || ex.First == nil {
		t.Fatal("planted bug not found in 200 seeds")
	}
	if ex.First.Seed != 12 {
		t.Errorf("first failing seed = %d, want 12 (measured)", ex.First.Seed)
	}
	if ex.First.Stats.Crashes == 0 {
		t.Error("the lazy-flush bug needs a crash to show up")
	}
	t.Logf("kv-lazy: %d/%d seeds fail, first=%d, seeds=%v", ex.Failures, ex.Runs, ex.First.Seed, ex.FailingSeeds)
	t.Logf("repro: %s", ex.First.Repro)
}

func TestCorrectKVHasNoFalsePositives(t *testing.T) {
	ex, err := app.Explore(kvConfig(kv.Correct), 1, 500)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Failures != 0 {
		t.Fatalf("correct kv flagged on seeds %v; repro: %s", ex.FailingSeeds, ex.First.Repro)
	}
	harsh := domain.FaultPlan{MinDelay: 1, MaxDelay: 30, Drop: 200, Dup: 200, Reorder: 300, ReorderExtra: 60,
		Crash: 300, CrashEvery: 40, RestartMin: 5, RestartMax: 80, MaxCrashed: 2}
	cfg := kvConfig(kv.Correct)
	cfg.Plan = &harsh
	ex, err = app.Explore(cfg, 1, 200)
	if err != nil || ex.Failures != 0 {
		t.Fatalf("correct kv under harsh plan: err=%v failures=%d", err, ex.Failures)
	}
}

func TestRunDeterminismProperty(t *testing.T) {
	prop := func(seed uint64, lazy bool) bool {
		cfg := kvConfig(kv.Correct)
		if lazy {
			cfg = kvConfig(kv.LazyFlush)
		}
		cfg.Seed = seed
		a, err1 := app.Run(cfg)
		b, err2 := app.Run(cfg)
		return err1 == nil && err2 == nil && a.Trace.Format() == b.Trace.Format() &&
			a.History.Format() == b.History.Format() && a.Check == b.Check
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 40}); err != nil {
		t.Fatal(err)
	}
}

// Golden digest: any unintended change to scheduling, RNG or the network
// model changes this value (regression guard; update deliberately).
func TestGoldenTraceDigest(t *testing.T) {
	cfg := kvConfig(kv.Correct)
	cfg.Seed = 1
	r, err := app.Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const want = uint64(0x5a0a50ce10f04862)
	if got := r.Trace.Digest(); got != want {
		t.Fatalf("digest = %#x, want %#x", got, want)
	}
}

func TestFailingHistoryReplaysFromSeed(t *testing.T) {
	cfg := kvConfig(kv.LazyFlush)
	cfg.Seed = 12
	a, _ := app.Run(cfg)
	b, _ := app.Run(cfg)
	if !a.Failed() || !b.Failed() || a.Trace.Digest() != b.Trace.Digest() {
		t.Fatal("failing seed must reproduce identically")
	}
	h, err := domain.ParseHistory(a.History.Format())
	if err != nil || domain.Check(h).OK {
		t.Fatalf("recorded history must round-trip and still fail: %v", err)
	}
}
