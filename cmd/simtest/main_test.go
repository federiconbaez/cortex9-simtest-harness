package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(args ...string) (int, string, string) {
	var o, e bytes.Buffer
	code := run(args, &o, &e)
	return code, o.String(), e.String()
}

func TestCLISingleRunAndRepro(t *testing.T) {
	dir := t.TempDir()
	tr, hi := filepath.Join(dir, "t.trace"), filepath.Join(dir, "h.history")
	code, out, _ := runCLI("-sut", "kv-lazy", "-seed", "12", "-trace", tr, "-history", hi)
	if code != 1 || !strings.Contains(out, "FAIL seed=12") || !strings.Contains(out, "repro: go run ./cmd/simtest -sut kv-lazy") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if code, out, _ = runCLI("-check-history", hi); code != 1 || !strings.Contains(out, "not linearizable") {
		t.Fatalf("recorded history must fail the checker: %d %s", code, out)
	}
	b, err := os.ReadFile(tr) // #nosec G304 -- path inside t.TempDir()
	if err != nil || len(b) == 0 {
		t.Fatal("trace not written")
	}
	if code, out, _ = runCLI("-sut", "kv", "-seed", "12"); code != 0 || !strings.Contains(out, "OK linearizable") {
		t.Fatalf("correct kv: %d %s", code, out)
	}
}

func TestCLIExploreAndFaults(t *testing.T) {
	if code, out, _ := runCLI("-sut", "kv-lazy", "-seed", "1", "-seeds", "20"); code != 1 || !strings.Contains(out, "failures=") {
		t.Fatalf("%d %s", code, out)
	}
	if code, out, _ := runCLI("-sut", "kv", "-seeds", "5", "-faults", "drop=100,dup=100,reorder=100"); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	if code, _, _ := runCLI("-faults", "none"); code != 0 {
		t.Fatal("none")
	}
}

func TestCLIUsageErrors(t *testing.T) {
	cases := [][]string{
		{"-bogus"}, {"-sut", "nope"}, {"-faults", "drop=x"}, {"-check-history", "/nonexistent"},
		{"-replicas", "1", "-faults", "delay=0-0"}, {"-seeds", "2", "-faults", "max-crashed=-1"},
		{"-trace", "/nonexistent/dir/x"},
	}
	for _, c := range cases {
		if code, _, _ := runCLI(c...); code != 2 {
			t.Errorf("%v: want exit 2, got %d", c, code)
		}
	}
	bad := filepath.Join(t.TempDir(), "bad")
	_ = os.WriteFile(bad, []byte("garbage"), 0o600)
	if code, _, _ := runCLI("-check-history", bad); code != 2 {
		t.Fatal("garbage history")
	}
	good := filepath.Join(t.TempDir(), "good")
	_ = os.WriteFile(good, []byte("1 c0 0 2 put k v\n2 c1 3 4 get k v\n"), 0o600)
	if code, out, _ := runCLI("-check-history", good); code != 0 || !strings.Contains(out, "OK") {
		t.Fatal("good history")
	}
}
