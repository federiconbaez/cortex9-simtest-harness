package domain

import (
	"errors"
	"strconv"
	"testing"
	"testing/quick"
)

func op(id int, call, ret Time, kind OpKind, key, arg string) Op {
	o := Op{ID: id, Client: NodeID("c" + strconv.Itoa(id)), Call: call, Return: ret, Input: Input{Kind: kind, Key: key}}
	if kind == OpPut {
		o.Input.Value = arg
	} else {
		o.Output = arg
	}
	if ret == Never {
		o.Pending = true
	}
	return o
}

func TestCheckKnownHistories(t *testing.T) {
	cases := []struct {
		name string
		h    History
		ok   bool
	}{
		{"empty", nil, true},
		{"read absent", History{op(1, 0, 1, OpGet, "k", Absent)}, true},
		{"sequential", History{op(1, 0, 1, OpPut, "k", "a"), op(2, 2, 3, OpGet, "k", "a")}, true},
		{"stale read", History{op(1, 0, 1, OpPut, "k", "a"), op(2, 2, 3, OpGet, "k", Absent)}, false},
		{"concurrent either", History{op(1, 0, 5, OpPut, "k", "a"), op(2, 1, 2, OpGet, "k", Absent), op(3, 3, 4, OpGet, "k", "a")}, true},
		{"new then old", History{op(1, 0, 10, OpPut, "k", "a"), op(2, 1, 2, OpGet, "k", "a"), op(3, 3, 4, OpGet, "k", Absent)}, false},
		{"lost ack write", History{op(1, 0, 1, OpPut, "k", "a"), op(2, 2, 3, OpPut, "k", "b"), op(3, 4, 5, OpGet, "k", "a")}, false},
		{"pending put may apply", History{op(1, 0, Never, OpPut, "k", "a"), op(2, 5, 6, OpGet, "k", "a")}, true},
		{"pending put may not apply", History{op(1, 0, Never, OpPut, "k", "a"), op(2, 5, 6, OpGet, "k", Absent)}, true},
		{"pending get ignored", History{op(1, 0, Never, OpGet, "k", ""), op(2, 5, 6, OpGet, "k", Absent)}, true},
		{"keys independent", History{op(1, 0, 1, OpPut, "x", "a"), op(2, 2, 3, OpGet, "y", Absent)}, true},
		{"equal times concurrent", History{op(1, 0, 3, OpPut, "k", "a"), op(2, 3, 4, OpGet, "k", Absent)}, true},
	}
	for _, c := range cases {
		if got := Check(c.h); got.OK != c.ok {
			t.Errorf("%s: got %v want %v", c.name, got.OK, c.ok)
		} else if !c.ok && got.Key != "k" {
			t.Errorf("%s: failing key %q", c.name, got.Key)
		}
		if bf := bruteForce(c.h); bf != c.ok {
			t.Errorf("%s: brute force disagrees with table", c.name)
		}
	}
}

// bruteForce enumerates every order over the whole KV (not per key) that
// respects real-time precedence; pending puts are optional. Test oracle.
func bruteForce(h History) bool {
	var ops []Op
	for _, o := range h {
		if o.Pending && o.Input.Kind == OpGet {
			continue
		}
		if o.Pending {
			o.Return = Never
		}
		ops = append(ops, o)
	}
	placed := make([]bool, len(ops))
	var dfs func(state map[string]string, done int) bool
	dfs = func(state map[string]string, done int) bool {
		allCompleted := true
		for i, o := range ops {
			if !placed[i] && !o.Pending {
				allCompleted = false
			}
		}
		if allCompleted {
			return true
		}
		for i, o := range ops {
			if placed[i] {
				continue
			}
			ready := true
			for j, p := range ops {
				if j != i && !placed[j] && !p.Pending && p.Return < o.Call {
					ready = false
				}
			}
			if !ready {
				continue
			}
			next := state
			if o.Input.Kind == OpPut {
				next = map[string]string{}
				for k, v := range state {
					next[k] = v
				}
				next[o.Input.Key] = o.Input.Value
			} else {
				v, ok := state[o.Input.Key]
				if !ok {
					v = Absent
				}
				if v != o.Output {
					continue
				}
			}
			placed[i] = true
			if dfs(next, done+1) {
				placed[i] = false
				return true
			}
			placed[i] = false
		}
		return false
	}
	return dfs(map[string]string{}, 0)
}

func randomHistory(r *Rand) History {
	n := 1 + r.Intn(7)
	h := make(History, 0, n)
	vals := []string{"1", "2", "3"}
	for i := 0; i < n; i++ {
		call := Time(r.Range(0, 20))
		ret := call + Time(r.Range(0, 10))
		key := []string{"a", "b"}[r.Intn(2)]
		if r.Chance(150) {
			ret = Never
		}
		if r.Chance(500) {
			h = append(h, op(i, call, ret, OpPut, key, vals[r.Intn(3)]))
		} else {
			h = append(h, op(i, call, ret, OpGet, key, append([]string{Absent}, vals...)[r.Intn(4)]))
		}
	}
	return h
}

func TestCheckerAgreesWithBruteForceProperty(t *testing.T) {
	yes, no := 0, 0
	prop := func(seed uint64) bool {
		h := randomHistory(NewRand(seed))
		got := Check(h).OK
		if got {
			yes++
		} else {
			no++
		}
		return got == bruteForce(h)
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 3000}); err != nil {
		t.Fatal(err)
	}
	if yes < 100 || no < 100 {
		t.Fatalf("generator unbalanced: %d linearizable, %d not", yes, no)
	}
	t.Logf("checker == brute force on 3000 histories (%d linearizable, %d not)", yes, no)
}

func TestHistoryFormatParseAndValidate(t *testing.T) {
	h := History{op(1, 0, 4, OpPut, "k", "v1"), op(2, 1, Never, OpGet, "k", ""), op(3, 2, 3, OpGet, "k", Absent), op(4, 5, Never, OpPut, "j", "v2")}
	txt := h.Format()
	got, err := ParseHistory(txt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Format() != txt || len(got.Keys()) != 2 {
		t.Fatalf("round trip:\n%s\n%s", txt, got.Format())
	}
	bad := []string{
		"1 c 0 1 put k",                    // fields
		"x c 0 1 put k v",                  // id
		"1 c 0 z put k v",                  // return
		"1 c 0 1 del k v",                  // kind
		"1 c 0 pending get k v",            // pending get output
		"1 c 5 1 put k v",                  // return < call
		"1 c 0 1 put k -",                  // absent as value
		"1 c 0 1 put k v\n1 c 0 1 put k v", // dup id
		"1 c 0 1 get k ?",                  // unknown output on completed get
		"1 c -1 1 put k v",                 // negative call
	}
	for _, b := range bad {
		if _, err := ParseHistory(b); err == nil {
			t.Errorf("%q accepted", b)
		}
	}
	if err := (History{{ID: 1, Client: "c", Input: Input{Key: "k"}}}).Validate(); !errors.Is(err, ErrInvalidHistory) {
		t.Fatal("unknown kind must be rejected")
	}
	if err := (History{{ID: 1, Client: "", Input: Input{Kind: OpGet, Key: "k"}, Output: "-"}}).Validate(); err == nil {
		t.Fatal("bad client")
	}
	if err := (History{{ID: 1, Client: "c", Input: Input{Kind: OpGet, Key: ""}, Output: "-"}}).Validate(); err == nil {
		t.Fatal("bad key")
	}
	if OpKind(9).String() != "invalid" {
		t.Fatal("kind string")
	}
}
