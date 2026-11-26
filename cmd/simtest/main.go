// Command simtest runs the deterministic simulation harness against the
// reference KV (or its planted-bug variant) and checks linearizability.
//
//	simtest -sut kv-lazy -seed 12                 # one run
//	simtest -sut kv-lazy -seed 1 -seeds 200       # explore 200 seeds
//	simtest -check-history run.history            # check a recorded history
//
// Exit status: 0 linearizable, 1 violation found, 2 usage or config error.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/federiconbaez/cortex9-simtest-harness/internal/adapters/kv"
	"github.com/federiconbaez/cortex9-simtest-harness/internal/adapters/memstore"
	"github.com/federiconbaez/cortex9-simtest-harness/internal/app"
	"github.com/federiconbaez/cortex9-simtest-harness/internal/domain"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("simtest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sut := fs.String("sut", "kv", "system under test: kv | kv-lazy")
	replicas := fs.Int("replicas", 3, "number of KV servers (n0 is the primary)")
	seed := fs.Uint64("seed", 1, "seed (first seed when -seeds > 1)")
	seeds := fs.Int("seeds", 1, "number of consecutive seeds to explore")
	clients := fs.Int("clients", app.DefaultClients, "concurrent clients")
	ops := fs.Int("ops", app.DefaultOpsPerClient, "operations per client")
	keys := fs.Int("keys", app.DefaultKeys, "distinct keys")
	horizon := fs.Int64("horizon", app.DefaultHorizon, "virtual ticks to simulate")
	think := fs.Int64("think", app.DefaultThinkMax, "max think time between ops")
	faults := fs.String("faults", "", "fault plan spec (empty: derived from seed; 'none': no faults)")
	traceOut := fs.String("trace", "", "write the trace of a single run to this file")
	histOut := fs.String("history", "", "write the history of a single run to this file")
	checkFile := fs.String("check-history", "", "only check a history file and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *checkFile != "" {
		return checkHistory(*checkFile, stdout, stderr)
	}
	sys, err := kv.ByName(*sut, *replicas)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cfg := app.Config{Seed: *seed, SUT: sys, NewStorage: func() domain.Storage { return memstore.New() },
		Clients: *clients, OpsPerClient: *ops, Keys: *keys, Horizon: domain.Time(*horizon), ThinkMax: domain.Duration(*think)}
	if *faults != "" {
		p, err := domain.ParseFaultPlan(*faults)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		cfg.Plan = &p
	}
	if *seeds > 1 {
		ex, err := app.Explore(cfg, *seed, *seeds)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		fmt.Fprintf(stdout, "sut=%s seeds=%d..%d runs=%d failures=%d failing=%v\n", sys.Name(), *seed, *seed+uint64(*seeds)-1, ex.Runs, ex.Failures, ex.FailingSeeds)
		if ex.First != nil {
			fmt.Fprintf(stdout, "FAIL seed=%d key=%s repro: %s\n", ex.First.Seed, ex.First.Check.Key, ex.First.Repro)
			return 1
		}
		return 0
	}
	r, err := app.Run(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := writeOut(*traceOut, r.Trace.Format()); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := writeOut(*histOut, r.History.Format()); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	st := r.Stats
	fmt.Fprintf(stdout, "sut=%s seed=%d plan=%s\n", sys.Name(), r.Seed, r.Plan)
	fmt.Fprintf(stdout, "events=%d sent=%d dropped=%d dup=%d reordered=%d crashes=%d ops=%d explored=%d digest=%016x\n",
		st.Dispatched, st.Sent, st.Dropped, st.Duplicated, st.Reordered, st.Crashes, r.Check.Ops, r.Check.Explored, r.Trace.Digest())
	if r.Plan.Partition > 0 {
		fmt.Fprintf(stdout, "partitions=%d lost-to-partition=%d\n", st.Partitions, st.LostToPartition)
	}
	if r.Failed() {
		fmt.Fprintf(stdout, "FAIL seed=%d key=%s repro: %s\n", r.Seed, r.Check.Key, r.Repro)
		return 1
	}
	fmt.Fprintln(stdout, "OK linearizable")
	return 0
}

func writeOut(path, content string) error {
	if path == "" {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func checkHistory(path string, stdout, stderr io.Writer) int {
	b, err := os.ReadFile(path) // #nosec G304 -- path is an explicit CLI argument
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	h, err := domain.ParseHistory(string(b))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	res := domain.Check(h)
	if !res.OK {
		fmt.Fprintf(stdout, "FAIL not linearizable: key=%s ops=%d explored=%d\n", res.Key, res.Ops, res.Explored)
		return 1
	}
	fmt.Fprintf(stdout, "OK linearizable: ops=%d explored=%d\n", res.Ops, res.Explored)
	return 0
}
