package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// FaultPlan configures every fault the simulator injects. Probabilities are
// expressed per mille (0..1000) so plans print and parse exactly.
type FaultPlan struct {
	MinDelay, MaxDelay Duration // one-way message latency range
	Drop               int      // per-mille chance a message is dropped
	Dup                int      // per-mille chance a message is delivered twice
	Reorder            int      // per-mille chance a message skips FIFO with extra delay
	ReorderExtra       Duration // maximum extra delay of a reordered message
	Crash              int      // per-mille chance, per crashable node and fault tick, of a crash
	CrashEvery         Duration // fault tick period
	RestartMin         Duration // minimum downtime after a crash
	RestartMax         Duration // maximum downtime after a crash
	MaxCrashed         int      // maximum number of nodes down at once
}

// NoFaults is a reliable FIFO network with one tick of latency and no crashes.
func NoFaults() FaultPlan {
	return FaultPlan{MinDelay: 1, MaxDelay: 1, ReorderExtra: 1, CrashEvery: 1, RestartMin: 1, RestartMax: 1}
}

// RandomFaultPlan derives a plan from r; the same seed yields the same plan.
func RandomFaultPlan(r *Rand) FaultPlan {
	p := FaultPlan{}
	p.MinDelay = Duration(r.Range(1, 3))
	p.MaxDelay = p.MinDelay + Duration(r.Range(0, 20))
	p.Drop = int(r.Range(0, 150))
	p.Dup = int(r.Range(0, 100))
	p.Reorder = int(r.Range(0, 200))
	p.ReorderExtra = Duration(r.Range(1, 50))
	p.Crash = int(r.Range(0, 100))
	p.CrashEvery = Duration(r.Range(50, 150))
	p.RestartMin = Duration(r.Range(10, 50))
	p.RestartMax = p.RestartMin + Duration(r.Range(0, 100))
	p.MaxCrashed = 1
	return p
}

// Validate checks ranges and returns an error wrapping ErrInvalidPlan.
func (p FaultPlan) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidPlan, fmt.Sprintf(format, a...))
	}
	probs := []struct {
		name string
		v    int
	}{{"drop", p.Drop}, {"dup", p.Dup}, {"reorder", p.Reorder}, {"crash", p.Crash}}
	for _, pr := range probs {
		if pr.v < 0 || pr.v > 1000 {
			return bad("%s=%d outside 0..1000", pr.name, pr.v)
		}
	}
	switch {
	case p.MinDelay < 1:
		return bad("min delay must be >= 1")
	case p.MaxDelay < p.MinDelay:
		return bad("max delay < min delay")
	case p.ReorderExtra < 1:
		return bad("reorder-extra must be >= 1")
	case p.CrashEvery < 1:
		return bad("crash-every must be >= 1")
	case p.RestartMin < 1 || p.RestartMax < p.RestartMin:
		return bad("restart range invalid")
	case p.MaxCrashed < 0:
		return bad("max-crashed must be >= 0")
	}
	return nil
}

// String renders the canonical spec accepted by ParseFaultPlan.
func (p FaultPlan) String() string {
	return fmt.Sprintf("delay=%d-%d,drop=%d,dup=%d,reorder=%d,reorder-extra=%d,crash=%d,crash-every=%d,restart=%d-%d,max-crashed=%d",
		p.MinDelay, p.MaxDelay, p.Drop, p.Dup, p.Reorder, p.ReorderExtra, p.Crash, p.CrashEvery, p.RestartMin, p.RestartMax, p.MaxCrashed)
}

// ParseFaultPlan parses a comma-separated spec. Missing keys keep the
// NoFaults defaults. The result is validated.
func ParseFaultPlan(spec string) (FaultPlan, error) {
	p := NoFaults()
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "none" {
		return p, nil
	}
	for _, item := range strings.Split(spec, ",") {
		k, v, ok := strings.Cut(item, "=")
		if !ok {
			return FaultPlan{}, fmt.Errorf("%w: fault item %q lacks '='", ErrParse, item)
		}
		if err := p.set(strings.TrimSpace(k), strings.TrimSpace(v)); err != nil {
			return FaultPlan{}, err
		}
	}
	if err := p.Validate(); err != nil {
		return FaultPlan{}, err
	}
	return p, nil
}

func (p *FaultPlan) set(k, v string) error {
	num := func() (int64, error) {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("%w: %s=%q: %w", ErrParse, k, v, err)
		}
		return n, nil
	}
	rng := func() (int64, int64, error) {
		a, b, ok := strings.Cut(v, "-")
		if !ok {
			return 0, 0, fmt.Errorf("%w: %s=%q expects lo-hi", ErrParse, k, v)
		}
		lo, err1 := strconv.ParseInt(a, 10, 32)
		hi, err2 := strconv.ParseInt(b, 10, 32)
		if err1 != nil || err2 != nil {
			return 0, 0, fmt.Errorf("%w: %s=%q is not a numeric range", ErrParse, k, v)
		}
		return lo, hi, nil
	}
	switch k {
	case "delay", "restart":
		lo, hi, err := rng()
		if err != nil {
			return err
		}
		if k == "delay" {
			p.MinDelay, p.MaxDelay = Duration(lo), Duration(hi)
		} else {
			p.RestartMin, p.RestartMax = Duration(lo), Duration(hi)
		}
		return nil
	}
	n, err := num()
	if err != nil {
		return err
	}
	switch k {
	case "drop":
		p.Drop = int(n)
	case "dup":
		p.Dup = int(n)
	case "reorder":
		p.Reorder = int(n)
	case "reorder-extra":
		p.ReorderExtra = Duration(n)
	case "crash":
		p.Crash = int(n)
	case "crash-every":
		p.CrashEvery = Duration(n)
	case "max-crashed":
		p.MaxCrashed = int(n)
	default:
		return fmt.Errorf("%w: unknown fault key %q", ErrParse, k)
	}
	return nil
}
