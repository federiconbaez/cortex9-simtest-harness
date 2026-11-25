package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/federiconbaez/cortex9-simtest-harness/internal/domain"
)

// Hand-written fakes: a single-node register (linearizable by construction)
// and a client that can be told to lie about reads.
type fakeStore map[string]string

func (s fakeStore) Get(k string) (string, bool) { v, ok := s[k]; return v, ok }
func (s fakeStore) Apply(b []domain.KV) {
	for _, kv := range b {
		s[kv.Key] = kv.Value
	}
}

type regServer struct{}

func (regServer) Start(domain.Env)         {}
func (regServer) Timer(domain.Env, uint64) {}
func (regServer) Receive(env domain.Env, from domain.NodeID, p []byte) {
	f := strings.Fields(string(p))
	switch f[0] {
	case "put":
		env.Storage().Apply([]domain.KV{{Key: f[1], Value: f[2]}})
		env.Send(from, []byte("-"))
	case "get":
		v, ok := env.Storage().Get(f[1])
		if !ok {
			v = domain.Absent
		}
		env.Send(from, []byte(v))
	}
}

type regClient struct {
	done func(string)
	lie  bool
}

func (c *regClient) Start(domain.Env)         {}
func (c *regClient) Timer(domain.Env, uint64) {}
func (c *regClient) Receive(_ domain.Env, _ domain.NodeID, p []byte) {
	if c.done != nil {
		d := c.done
		c.done = nil
		out := string(p)
		if c.lie && out != domain.Absent {
			out = "forged"
		}
		d(out)
	}
}
func (c *regClient) Invoke(env domain.Env, in domain.Input, done func(string)) {
	c.done = done
	msg := "get " + in.Key
	if in.Kind == domain.OpPut {
		msg = "put " + in.Key + " " + in.Value
	}
	env.Send("s0", []byte(msg))
}

type regSUT struct{ lie bool }

func (regSUT) Name() string                            { return "reg" }
func (regSUT) Servers() []domain.NodeID                { return []domain.NodeID{"s0"} }
func (regSUT) NewServer(domain.NodeID) domain.Process  { return regServer{} }
func (s regSUT) NewClient(domain.NodeID) domain.Client { return &regClient{lie: s.lie} }

func cfgFor(s domain.SUT) Config {
	none := domain.NoFaults()
	return Config{SUT: s, NewStorage: func() domain.Storage { return fakeStore{} }, Plan: &none}
}

func TestRunRecordsCompleteHistory(t *testing.T) {
	r, err := Run(cfgFor(regSUT{}))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Check.OK || len(r.History) != DefaultClients*DefaultOpsPerClient {
		t.Fatalf("ok=%v ops=%d", r.Check.OK, len(r.History))
	}
	for _, op := range r.History {
		if op.Pending || op.Return < op.Call {
			t.Fatalf("bad op %+v", op)
		}
	}
	if !strings.Contains(r.Repro, "-faults 'delay=1-1") || !strings.Contains(r.Trace.Format(), "invoke put") {
		t.Fatal("repro must carry the explicit plan and the trace the invocations")
	}
}

func TestRunDetectsLyingSUT(t *testing.T) {
	ex, err := Explore(cfgFor(regSUT{lie: true}), 1, 5)
	if err != nil || ex.Failures != 5 || len(ex.FailingSeeds) != 5 || ex.First == nil || ex.First.Seed != 1 {
		t.Fatalf("ex=%+v err=%v", ex, err)
	}
}

func TestRunErrors(t *testing.T) {
	if _, err := Run(Config{}); !errors.Is(err, domain.ErrInvalidConfig) {
		t.Fatal("missing SUT")
	}
	bad := domain.FaultPlan{}
	c := cfgFor(regSUT{})
	c.Plan = &bad
	if _, err := Run(c); !errors.Is(err, domain.ErrInvalidPlan) {
		t.Fatal("invalid plan")
	}
	if _, err := Explore(c, 1, 1); err == nil {
		t.Fatal("explore must surface errors")
	}
}

// procSUT's clients accept Invoke but never send anything, so every
// client's first operation stays pending until the horizon.
type procOnly struct{ regServer }

type procSUT struct{ regSUT }

func (procSUT) NewClient(domain.NodeID) domain.Client { return clientShim{procOnly{}} }

type clientShim struct{ domain.Process }

func (clientShim) Invoke(domain.Env, domain.Input, func(string)) {}

func TestPendingOpsWhenSUTNeverAnswers(t *testing.T) {
	r, err := Run(cfgFor(procSUT{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.History) != DefaultClients || !r.Check.OK {
		t.Fatalf("each client should have exactly one pending op: %d", len(r.History))
	}
	for _, op := range r.History {
		if !op.Pending {
			t.Fatal("op should be pending")
		}
	}
}
