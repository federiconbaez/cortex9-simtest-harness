package domain

import (
	"strconv"
	"strings"
)

// mapStorage is a hand-written fake of the Storage port.
type mapStorage struct{ m map[string]string }

func newMapStorage() *mapStorage { return &mapStorage{m: map[string]string{}} }

func (s *mapStorage) Get(k string) (string, bool) { v, ok := s.m[k]; return v, ok }
func (s *mapStorage) Apply(b []KV) {
	for _, kv := range b {
		s.m[kv.Key] = kv.Value
	}
}

// pinger sends numbered pings to peer, retries on a timer and persists the
// highest pong it has seen. It exercises Send, After, Storage and Rand.
type pinger struct {
	peer NodeID
	n    int
}

func (p *pinger) Start(env Env) {
	if v, ok := env.Storage().Get("pong"); ok {
		p.n, _ = strconv.Atoi(v)
	}
	env.Send(p.peer, []byte("ping "+strconv.Itoa(p.n)))
	env.After(Duration(5+env.Rand().Intn(5)), 1)
}

func (p *pinger) Receive(env Env, _ NodeID, payload []byte) {
	f := strings.Fields(string(payload))
	if len(f) == 2 && f[0] == "pong" {
		n, _ := strconv.Atoi(f[1])
		if n >= p.n {
			p.n = n + 1
			env.Storage().Apply([]KV{{Key: "pong", Value: strconv.Itoa(n)}})
			env.Send(p.peer, []byte("ping "+strconv.Itoa(p.n)))
		}
	}
}

func (p *pinger) Timer(env Env, tag uint64) {
	env.Send(p.peer, []byte("ping "+strconv.Itoa(p.n)))
	env.After(10, tag)
}

// echo answers every ping with a pong carrying the same number.
type echo struct{ got []string }

func (e *echo) Start(Env) {}
func (e *echo) Receive(env Env, from NodeID, payload []byte) {
	e.got = append(e.got, string(payload))
	env.Send(from, []byte(strings.Replace(string(payload), "ping", "pong", 1)))
}
func (e *echo) Timer(Env, uint64) {}

func pingPongSim(seed uint64, plan FaultPlan, horizon Time) (*Sim, error) {
	s, err := NewSim(SimConfig{Seed: seed, Horizon: horizon, Plan: plan})
	if err != nil {
		return nil, err
	}
	if err := s.AddNode("a", func() Process { return &pinger{peer: "b"} }, newMapStorage(), true); err != nil {
		return nil, err
	}
	if err := s.AddNode("b", func() Process { return &echo{} }, newMapStorage(), true); err != nil {
		return nil, err
	}
	return s, s.Run()
}

var chaos = FaultPlan{MinDelay: 1, MaxDelay: 9, Drop: 100, Dup: 100, Reorder: 150, ReorderExtra: 20,
	Crash: 200, CrashEvery: 40, RestartMin: 5, RestartMax: 30, MaxCrashed: 1}
