package domain

import (
	"errors"
	"strings"
	"testing"
	"testing/quick"
)

func TestRandDeterministicAndBounded(t *testing.T) {
	a, b := NewRand(42), NewRand(42)
	for i := 0; i < 100; i++ {
		if a.Uint64() != b.Uint64() {
			t.Fatal("same seed diverged")
		}
	}
	// Golden value pins the sequence across Go versions (regression).
	if got := NewRand(1).Uint64(); got != 0x910a2dec89025cc1 {
		t.Fatalf("splitmix64(1) = %#x", got)
	}
	prop := func(seed uint64, lo int16, span uint8) bool {
		r := NewRand(seed)
		l, h := int64(lo), int64(lo)+int64(span)
		v := r.Range(l, h)
		return v >= l && v <= h && r.Intn(int(span)+1) <= int(span)
	}
	if err := quick.Check(prop, nil); err != nil {
		t.Fatal(err)
	}
	r := NewRand(7)
	if r.Intn(0) != 0 || r.Range(5, 5) != 5 || r.Chance(0) || !r.Chance(1000) {
		t.Fatal("edge cases")
	}
	if NewRand(3).Fork().Uint64() == NewRand(3).Uint64() {
		t.Fatal("fork should not replay the parent stream")
	}
}

func TestClockMonotonic(t *testing.T) {
	var c Clock
	if err := c.AdvanceTo(5); err != nil || c.Now() != 5 {
		t.Fatal("advance")
	}
	if err := c.AdvanceTo(4); !errors.Is(err, ErrTimeTravel) {
		t.Fatalf("want ErrTimeTravel, got %v", err)
	}
}

func TestFaultPlanRoundTripProperty(t *testing.T) {
	prop := func(seed uint64) bool {
		p := RandomFaultPlan(NewRand(seed))
		if p.Validate() != nil {
			return false
		}
		q, err := ParseFaultPlan(p.String())
		return err == nil && q == p
	}
	if err := quick.Check(prop, nil); err != nil {
		t.Fatal(err)
	}
	if p, err := ParseFaultPlan("none"); err != nil || p != NoFaults() {
		t.Fatal("none")
	}
	if p, err := ParseFaultPlan("drop=10, delay=2-4"); err != nil || p.Drop != 10 || p.MaxDelay != 4 {
		t.Fatalf("partial spec: %v %v", p, err)
	}
	for _, bad := range []string{"drop", "drop=x", "drop=2000", "delay=3", "delay=a-b", "delay=5-2",
		"zzz=1", "restart=0-1", "crash-every=0", "max-crashed=-1", "reorder-extra=0", "delay=0-1"} {
		if _, err := ParseFaultPlan(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestNetworkFIFOWithoutReorderProperty(t *testing.T) {
	prop := func(seed uint64, gaps []uint8) bool {
		r := NewRand(seed)
		plan := RandomFaultPlan(r)
		plan.Reorder, plan.Dup = 0, 0
		n := NewFaultyNetwork(plan)
		now, last := Time(0), Time(-1)
		for i, g := range gaps {
			now += Time(g % 4)
			for _, d := range n.Route(Message{ID: uint64(i), From: "a", To: "b"}, now, r) {
				if d.At < last || d.At < now+Time(plan.MinDelay) {
					return false
				}
				last = d.At
			}
		}
		return true
	}
	if err := quick.Check(prop, nil); err != nil {
		t.Fatal(err)
	}
	r := NewRand(1)
	all := FaultPlan{MinDelay: 1, MaxDelay: 1, Drop: 1000, ReorderExtra: 1, CrashEvery: 1, RestartMin: 1, RestartMax: 1}
	if len(NewFaultyNetwork(all).Route(Message{}, 0, r)) != 0 {
		t.Fatal("drop=1000 must drop")
	}
	all.Drop, all.Dup, all.Reorder = 0, 1000, 1000
	ds := NewFaultyNetwork(all).Route(Message{}, 0, r)
	if len(ds) != 2 || !ds[1].Duplicate || !ds[0].Reordered {
		t.Fatalf("dup+reorder: %+v", ds)
	}
}

func TestSimDeterminismProperty(t *testing.T) {
	prop := func(seed uint64) bool {
		a, err1 := pingPongSim(seed, chaos, 400)
		b, err2 := pingPongSim(seed, chaos, 400)
		return err1 == nil && err2 == nil && a.Trace().Format() == b.Trace().Format() &&
			a.Trace().Digest() == b.Trace().Digest() && a.Stats() == b.Stats()
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 50}); err != nil {
		t.Fatal(err)
	}
	a, _ := pingPongSim(1, chaos, 400)
	b, _ := pingPongSim(2, chaos, 400)
	if a.Trace().Digest() == b.Trace().Digest() {
		t.Fatal("different seeds should explore different schedules")
	}
}

func TestSchedulerOrderingAndFairnessProperty(t *testing.T) {
	prop := func(seed uint64) bool {
		s, err := pingPongSim(seed, chaos, 300)
		if err != nil || s.PendingWithinHorizon() != 0 {
			return false
		}
		var pa Time
		var ps uint64
		for i, e := range s.Trace() {
			if e.At > 300 {
				return false
			}
			if i > 0 && (e.At < pa || (e.At == pa && e.Seq < ps)) {
				return false // I2: (at, seq) non-decreasing
			}
			pa, ps = e.At, e.Seq
		}
		st := s.Stats()
		return st.Dispatched <= st.Scheduled && st.Sent == st.Dropped+st.Delivered+st.LostToCrash-st.Duplicated+s.inFlight()
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 50}); err != nil {
		t.Fatal(err)
	}
}

// inFlight counts deliveries still queued (beyond the horizon).
func (s *Sim) inFlight() int {
	n := 0
	for _, e := range s.queue {
		if e.kind == evDeliver {
			n++
		}
	}
	return n
}

func TestSchedulerFIFOAtEqualTimes(t *testing.T) {
	s, _ := NewSim(SimConfig{Seed: 1, Horizon: 10, Plan: NoFaults()})
	rec := &echo{}
	_ = s.AddNode("x", func() Process { return rec }, nil, false)
	var order []string
	for _, l := range []string{"c1", "c2", "c3"} {
		l := l
		_ = s.Call(5, "x", l, func(Env, Process) { order = append(order, l) })
	}
	if err := s.Run(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "c1,c2,c3" {
		t.Fatalf("equal-time events not FIFO: %v", order)
	}
}

// timerProbe arms a timer at start; it records which incarnation fired.
type timerProbe struct {
	fired *[]string
	inc   string
}

func (p *timerProbe) Start(env Env)                { env.After(50, 9) }
func (p *timerProbe) Receive(Env, NodeID, []byte)  {}
func (p *timerProbe) Timer(env Env, _ uint64)      { *p.fired = append(*p.fired, p.inc) }
func (p *timerProbe) String() string               { return p.inc }
func newProbe(f *[]string, inc string) *timerProbe { return &timerProbe{fired: f, inc: inc} }

func TestCrashDropsOldTimersAndKeepsStorage(t *testing.T) {
	var fired []string
	inc := 0
	plan := NoFaults()
	plan.Crash, plan.CrashEvery, plan.RestartMin, plan.RestartMax, plan.MaxCrashed = 1000, 10, 100, 100, 1
	st := newMapStorage()
	s, _ := NewSim(SimConfig{Seed: 1, Horizon: 60, Plan: plan})
	_ = s.AddNode("n", func() Process {
		inc++
		st.Apply([]KV{{Key: "boots", Value: strings.Repeat("x", inc)}})
		return newProbe(&fired, strings.Repeat("x", inc))
	}, st, true)
	if err := s.Run(); err != nil {
		t.Fatal(err)
	}
	// Crashed at t=10, restart scheduled at t=110 > horizon: the t=50 timer
	// of the first incarnation must not fire (I6).
	if len(fired) != 0 || s.Stats().Crashes != 1 {
		t.Fatalf("fired=%v stats=%+v", fired, s.Stats())
	}
	s2, _ := NewSim(SimConfig{Seed: 1, Horizon: 400, Plan: plan})
	_ = s2.AddNode("n", func() Process { inc++; return newProbe(&fired, "late") }, st, true)
	_ = s2.Run()
	if v, _ := st.Get("boots"); v != "x" { // I7: storage survives restarts
		t.Fatalf("storage lost: %q", v)
	}
	if s2.Stats().Restarts == 0 {
		t.Fatal("expected restarts")
	}
}

func TestSimErrorsAndEdgeCases(t *testing.T) {
	if _, err := NewSim(SimConfig{Horizon: 0, Plan: NoFaults()}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("horizon")
	}
	if _, err := NewSim(SimConfig{Horizon: 1}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatal("plan")
	}
	s, _ := NewSim(SimConfig{Horizon: 50, Plan: NoFaults()})
	if err := s.AddNode("bad id", nil, nil, false); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("token")
	}
	_ = s.AddNode("a", func() Process { return &pinger{peer: "ghost"} }, newMapStorage(), false)
	if err := s.AddNode("a", nil, nil, false); !errors.Is(err, ErrDuplicateNode) {
		t.Fatal("dup")
	}
	if err := s.Call(1, "nope", "x", nil); !errors.Is(err, ErrUnknownNode) {
		t.Fatal("unknown")
	}
	if err := s.Run(); err != nil {
		t.Fatal(err)
	}
	if err := s.Run(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("run twice")
	}
	if err := s.AddNode("b", nil, nil, false); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("add after run")
	}
	if s.Stats().Dropped == 0 || !strings.Contains(s.Trace().Format(), "unknown") {
		t.Fatal("send to unknown node must be dropped and traced")
	}
	s.Note("a", "")
	if s.Now() != 50 && s.Now() > 50 {
		t.Fatal("clock beyond horizon")
	}
}

func TestCallSkippedWhileDown(t *testing.T) {
	plan := NoFaults()
	plan.Crash, plan.CrashEvery, plan.RestartMin, plan.RestartMax, plan.MaxCrashed = 1000, 5, 50, 50, 1
	s, _ := NewSim(SimConfig{Seed: 3, Horizon: 100, Plan: plan})
	_ = s.AddNode("n", func() Process { return &echo{} }, nil, true)
	ran := false
	_ = s.Call(10, "n", "probe", func(Env, Process) { ran = true })
	_ = s.Run()
	if ran || !strings.Contains(s.Trace().Format(), "skipped probe") {
		t.Fatal("call on a crashed node must be skipped")
	}
}

func TestTraceRoundTrip(t *testing.T) {
	s, _ := pingPongSim(9, chaos, 200)
	txt := s.Trace().Format()
	tr, err := ParseTrace(txt)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Format() != txt {
		t.Fatal("round trip changed the trace")
	}
	for _, bad := range []string{"1 2 3", "x 1 send a d", "1 x send a d", "1 1 bogus a d", "1 1 send a\x01 d", "1 1 send a ", "-1 1 send a d", "1 1 send a d\te"} {
		if _, err := ParseTrace(bad); !errors.Is(err, ErrParse) {
			t.Errorf("%q accepted", bad)
		}
	}
}
