package kv

import (
	"errors"
	"strings"
	"testing"

	"github.com/federiconbaez/cortex9-simtest-harness/internal/domain"
)

// fakeEnv is a hand-written Env that records sends and timers.
type fakeEnv struct {
	self   domain.NodeID
	store  map[string]string
	sent   []string
	timers []uint64
}

func newEnv(self domain.NodeID) *fakeEnv { return &fakeEnv{self: self, store: map[string]string{}} }

func (e *fakeEnv) Self() domain.NodeID { return e.self }
func (e *fakeEnv) Now() domain.Time    { return 0 }
func (e *fakeEnv) Send(to domain.NodeID, p []byte) {
	e.sent = append(e.sent, string(to)+":"+string(p))
}
func (e *fakeEnv) After(_ domain.Duration, tag uint64) { e.timers = append(e.timers, tag) }
func (e *fakeEnv) Storage() domain.Storage             { return e }
func (e *fakeEnv) Rand() *domain.Rand                  { return domain.NewRand(1) }
func (e *fakeEnv) Get(k string) (string, bool)         { v, ok := e.store[k]; return v, ok }
func (e *fakeEnv) Apply(b []domain.KV) {
	for _, kv := range b {
		e.store[kv.Key] = kv.Value
	}
}
func (e *fakeEnv) take() []string { s := e.sent; e.sent = nil; return s }

func TestCorrectPrimaryPersistsReplicatesThenAcks(t *testing.T) {
	sys := New(Correct, 3)
	env := newEnv(Primary)
	p := sys.NewServer(Primary)
	p.Start(env)
	p.Receive(env, "c0", []byte("put 1 k v"))
	if env.store["v/k"] != "v" || env.store["c/c0"] != "1" {
		t.Fatalf("not durable before replication: %v", env.store)
	}
	if got := strings.Join(env.take(), "|"); got != "n1:rep 1 k v|n2:rep 1 k v" {
		t.Fatalf("replication: %s", got)
	}
	p.Receive(env, "c0", []byte("put 1 k v")) // retry while replicating: silent
	p.Receive(env, "n1", []byte("ack 1"))
	if len(env.take()) != 0 {
		t.Fatal("acked before all backups")
	}
	p.Timer(env, tagRetry)
	if got := strings.Join(env.take(), "|"); got != "n2:rep 1 k v" {
		t.Fatalf("retry should only target unacked backups: %s", got)
	}
	p.Receive(env, "n2", []byte("ack 1"))
	p.Receive(env, "n2", []byte("ack 1")) // duplicate ack ignored
	if got := strings.Join(env.take(), "|"); got != "c0:ok 1 -" {
		t.Fatalf("client ack: %s", got)
	}
	// After a crash the new incarnation dedupes the retried put from storage.
	p2 := sys.NewServer(Primary)
	p2.Start(env)
	p2.Receive(env, "c0", []byte("put 1 k other"))
	p2.Receive(env, "c1", []byte("get 7 k"))
	if got := strings.Join(env.take(), "|"); got != "c0:ok 1 -|c1:ok 7 v" || env.store["v/k"] != "v" {
		t.Fatalf("dedupe after restart: %s %v", got, env.store)
	}
	for _, junk := range []string{"", "put", "put x k v", "get 1", "zzz 1"} {
		p2.Receive(env, "c0", []byte(junk))
	}
	if len(env.take()) != 0 {
		t.Fatal("malformed messages must be ignored")
	}
}

func TestBackupAppliesNewerVersionsOnly(t *testing.T) {
	env := newEnv("n1")
	b := New(Correct, 3).NewServer("n1")
	b.Start(env)
	b.Receive(env, Primary, []byte("rep 2 k new"))
	b.Receive(env, Primary, []byte("rep 1 k old"))
	if env.store["v/k"] != "new" || len(env.take()) != 2 {
		t.Fatalf("backup state %v", env.store)
	}
}

func TestLazyVariantLosesAckedWriteOnCrash(t *testing.T) {
	sys := New(LazyFlush, 1)
	env := newEnv(Primary)
	p := sys.NewServer(Primary)
	p.Start(env)
	p.Receive(env, "c0", []byte("put 1 k v"))
	if got := strings.Join(env.take(), "|"); got != "c0:ok 1 -" || len(env.store) != 0 {
		t.Fatalf("lazy variant should ack from memory: %s %v", got, env.store)
	}
	p2 := sys.NewServer(Primary) // crash before the flush timer
	p2.Start(env)
	p2.Receive(env, "c1", []byte("get 2 k"))
	if got := strings.Join(env.take(), "|"); got != "c1:ok 2 -" {
		t.Fatalf("expected the acked write to be lost: %s", got)
	}
	p2.Receive(env, "c0", []byte("put 2 k w"))
	p2.Timer(env, tagFlush)
	if env.store["v/k"] != "w" || env.store["c/c0"] != "2" {
		t.Fatalf("flush: %v", env.store)
	}
}

func TestClientRetriesAndMatchesSeq(t *testing.T) {
	env := newEnv("c0")
	c := New(Correct, 1).NewClient("c0")
	c.Start(env)
	var got []string
	c.Invoke(env, domain.Input{Kind: domain.OpPut, Key: "k", Value: "v"}, func(o string) { got = append(got, o) })
	c.Invoke(env, domain.Input{Kind: domain.OpGet, Key: "k"}, func(string) { t.Fatal("overlapping invoke") })
	c.Timer(env, 1)
	c.Timer(env, 99)
	c.Receive(env, Primary, []byte("ok 9 x"))
	c.Receive(env, Primary, []byte("ok 1 -"))
	c.Receive(env, Primary, []byte("ok 1 -"))
	c.Timer(env, 1)
	if len(got) != 1 || strings.Join(env.take(), "|") != "n0:put 1 k v|n0:put 1 k v" {
		t.Fatalf("got=%v", got)
	}
	c.Invoke(env, domain.Input{Kind: domain.OpGet, Key: "k"}, func(o string) { got = append(got, o) })
	c.Receive(env, Primary, []byte("ok 2 v"))
	if strings.Join(got, ",") != "-,v" {
		t.Fatalf("got=%v", got)
	}
}

func TestSystemNamesAndServers(t *testing.T) {
	if s, err := ByName("kv", 0); err != nil || s.Name() != "kv" || len(s.Servers()) != 1 {
		t.Fatal("kv")
	}
	if s, err := ByName("kv-lazy", 2); err != nil || s.Name() != "kv-lazy" || s.Servers()[1] != "n1" {
		t.Fatal("kv-lazy")
	}
	if _, err := ByName("x", 1); !errors.Is(err, domain.ErrInvalidConfig) {
		t.Fatal("unknown")
	}
}
