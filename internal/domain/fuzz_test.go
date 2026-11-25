package domain

import "testing"

func FuzzParseTrace(f *testing.F) {
	s, _ := pingPongSim(1, chaos, 60)
	f.Add(s.Trace().Format())
	f.Add("0 1 note a -\n")
	f.Add("3 4 send a id=1 to=b copies=2\n\n")
	f.Fuzz(func(t *testing.T, in string) {
		tr, err := ParseTrace(in)
		if err != nil {
			return
		}
		again, err := ParseTrace(tr.Format())
		if err != nil || again.Format() != tr.Format() {
			t.Fatalf("unstable round trip: %v", err)
		}
	})
}

func FuzzParseHistory(f *testing.F) {
	f.Add("1 c0 0 4 put k v1\n2 c1 1 pending get k ?\n3 c2 2 3 get k -\n")
	f.Add("1 c0 0 pending put k v\n")
	f.Fuzz(func(t *testing.T, in string) {
		h, err := ParseHistory(in)
		if err != nil {
			return
		}
		again, err := ParseHistory(h.Format())
		if err != nil || again.Format() != h.Format() {
			t.Fatalf("unstable round trip: %v", err)
		}
		if len(h) <= 6 && Check(h).OK != bruteForce(h) {
			t.Fatalf("checker disagrees with brute force on:\n%s", h.Format())
		}
	})
}

func FuzzParseFaultPlan(f *testing.F) {
	f.Add(RandomFaultPlan(NewRand(1)).String())
	f.Add("none")
	f.Add("drop=10,delay=1-3")
	f.Fuzz(func(t *testing.T, in string) {
		p, err := ParseFaultPlan(in)
		if err != nil {
			return
		}
		q, err := ParseFaultPlan(p.String())
		if err != nil || q != p {
			t.Fatalf("unstable round trip %q: %v", p.String(), err)
		}
	})
}
