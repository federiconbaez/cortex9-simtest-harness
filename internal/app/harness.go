// Package app wires the domain simulator, a SUT and a workload into a
// reproducible run, records the history and checks it. It depends only on
// domain ports; adapters are injected by the caller.
package app

import (
	"fmt"
	"strconv"

	"github.com/federiconbaez/cortex9-simtest-harness/internal/domain"
)

// Config describes one simulated run. Zero values get defaults.
type Config struct {
	Seed         uint64
	SUT          domain.SUT
	NewStorage   func() domain.Storage
	Clients      int
	OpsPerClient int
	Keys         int
	Horizon      domain.Time
	ThinkMax     domain.Duration
	// Plan overrides the seed-derived fault plan when non-nil.
	Plan *domain.FaultPlan
}

// Defaults used when the Config fields are zero.
const (
	DefaultClients      = 3
	DefaultOpsPerClient = 12
	DefaultKeys         = 2
	DefaultHorizon      = 3000
	DefaultThinkMax     = 40
)

func (c Config) withDefaults() (Config, error) {
	if c.SUT == nil || c.NewStorage == nil {
		return c, fmt.Errorf("%w: SUT and NewStorage are required", domain.ErrInvalidConfig)
	}
	if c.Clients <= 0 {
		c.Clients = DefaultClients
	}
	if c.OpsPerClient <= 0 {
		c.OpsPerClient = DefaultOpsPerClient
	}
	if c.Keys <= 0 {
		c.Keys = DefaultKeys
	}
	if c.Horizon <= 0 {
		c.Horizon = DefaultHorizon
	}
	if c.ThinkMax <= 0 {
		c.ThinkMax = DefaultThinkMax
	}
	return c, nil
}

// Report is the outcome of one run.
type Report struct {
	Seed    uint64
	Plan    domain.FaultPlan
	Trace   domain.Trace
	History domain.History
	Check   domain.CheckResult
	Stats   domain.Stats
	Repro   string
}

// Failed reports whether the history is not linearizable.
func (r Report) Failed() bool { return !r.Check.OK }

// ReproCommand renders a one-line command that replays cfg.
func ReproCommand(cfg Config) string {
	cmd := fmt.Sprintf("go run ./cmd/simtest -sut %s -replicas %d -seed %d -clients %d -ops %d -keys %d -horizon %d -think %d",
		cfg.SUT.Name(), len(cfg.SUT.Servers()), cfg.Seed, cfg.Clients, cfg.OpsPerClient, cfg.Keys, cfg.Horizon, cfg.ThinkMax)
	if cfg.Plan != nil {
		cmd += fmt.Sprintf(" -faults '%s'", cfg.Plan.String())
	}
	return cmd
}

// Run executes one deterministic simulation and checks its history.
// The seed is split into independent streams: fault plan, simulator, workload.
func Run(cfg Config) (Report, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return Report{}, err
	}
	master := domain.NewRand(cfg.Seed)
	planRng, simRng, workRng := master.Fork(), master.Fork(), master.Fork()
	plan := domain.RandomFaultPlan(planRng)
	if cfg.Plan != nil {
		plan = *cfg.Plan
	}
	sim, err := domain.NewSim(domain.SimConfig{Seed: simRng.Uint64(), Horizon: cfg.Horizon, Plan: plan})
	if err != nil {
		return Report{}, err
	}
	for _, id := range cfg.SUT.Servers() {
		id := id
		if err := sim.AddNode(id, func() domain.Process { return cfg.SUT.NewServer(id) }, cfg.NewStorage(), true); err != nil {
			return Report{}, err
		}
	}
	w := &workload{cfg: cfg, sim: sim, rng: workRng}
	for i := 0; i < cfg.Clients; i++ {
		id := domain.NodeID("c" + strconv.Itoa(i))
		c := cfg.SUT.NewClient(id)
		if err := sim.AddNode(id, func() domain.Process { return c }, cfg.NewStorage(), false); err != nil {
			return Report{}, err
		}
		if err := w.schedule(i, id, domain.Time(workRng.Range(0, int64(cfg.ThinkMax)))); err != nil {
			return Report{}, err
		}
	}
	if err := sim.Run(); err != nil {
		return Report{}, err
	}
	if w.err != nil {
		return Report{}, w.err
	}
	return Report{
		Seed: cfg.Seed, Plan: plan, Trace: sim.Trace(), History: w.history,
		Check: domain.Check(w.history), Stats: sim.Stats(), Repro: ReproCommand(cfg),
	}, nil
}

// workload issues one operation at a time per client, with random think
// time, unique put values and a small key space to create contention.
type workload struct {
	cfg     Config
	sim     *domain.Sim
	rng     *domain.Rand
	history domain.History
	issued  map[domain.NodeID]int
	err     error
}

func (w *workload) schedule(ci int, id domain.NodeID, at domain.Time) error {
	if w.issued == nil {
		w.issued = map[domain.NodeID]int{}
	}
	if w.issued[id] >= w.cfg.OpsPerClient {
		return nil
	}
	w.issued[id]++
	n := w.issued[id]
	in := domain.Input{Kind: domain.OpGet, Key: "k" + strconv.Itoa(w.rng.Intn(w.cfg.Keys))}
	if w.rng.Chance(500) {
		in.Kind, in.Value = domain.OpPut, fmt.Sprintf("v%dx%d", ci, n)
	}
	label := fmt.Sprintf("invoke %s %s %s", in.Kind, in.Key, in.Value)
	return w.sim.Call(at, id, label, func(env domain.Env, p domain.Process) {
		c, ok := p.(domain.Client)
		if !ok {
			w.err = fmt.Errorf("%w: node %s is not a domain.Client", domain.ErrInvalidConfig, id)
			return
		}
		idx := len(w.history)
		w.history = append(w.history, domain.Op{ID: idx, Client: id, Input: in, Call: env.Now(), Return: domain.Never, Pending: true})
		c.Invoke(env, in, func(out string) {
			op := &w.history[idx]
			op.Return, op.Pending = w.sim.Now(), false
			if in.Kind == domain.OpGet {
				op.Output = out
			}
			w.sim.Note(id, fmt.Sprintf("return %d %s", idx, out))
			next := w.sim.Now() + domain.Time(w.rng.Range(1, int64(w.cfg.ThinkMax)))
			if err := w.schedule(ci, id, next); err != nil && w.err == nil {
				w.err = err
			}
		})
	})
}

// Exploration summarises a sweep over consecutive seeds.
type Exploration struct {
	Runs         int
	Failures     int
	FailingSeeds []uint64 // first ten
	First        *Report  // first failing run, if any
}

// Explore runs count seeds starting at first and collects failures.
func Explore(cfg Config, first uint64, count int) (Exploration, error) {
	var ex Exploration
	for i := 0; i < count; i++ {
		c := cfg
		c.Seed = first + uint64(i)
		r, err := Run(c)
		if err != nil {
			return ex, err
		}
		ex.Runs++
		if r.Failed() {
			ex.Failures++
			if len(ex.FailingSeeds) < 10 {
				ex.FailingSeeds = append(ex.FailingSeeds, c.Seed)
			}
			if ex.First == nil {
				rr := r
				ex.First = &rr
			}
		}
	}
	return ex, nil
}
