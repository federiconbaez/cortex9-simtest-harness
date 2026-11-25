package domain

import (
	"sort"
	"strconv"
	"strings"
)

// CheckResult is the verdict of the linearizability checker.
type CheckResult struct {
	OK       bool
	Key      string // first key (sorted) whose sub-history is not linearizable
	Ops      int    // operations considered (pending gets are dropped)
	Explored int    // configurations explored across all keys
}

// Check decides whether h is linearizable with respect to a KV of
// registers whose initial value is Absent. It checks each key separately
// (linearizability is local) with the Wing-Gong/Lowe algorithm plus a cache
// of (linearized set, state). Pending puts may or may not take effect;
// pending gets are ignored.
func Check(h History) CheckResult {
	res := CheckResult{OK: true}
	byKey := map[string][]Op{}
	for _, op := range h {
		if op.Pending && op.Input.Kind == OpGet {
			continue
		}
		if op.Pending {
			op.Return = Never
		}
		byKey[op.Input.Key] = append(byKey[op.Input.Key], op)
		res.Ops++
	}
	for _, k := range h.Keys() {
		ok, explored := checkRegister(byKey[k])
		res.Explored += explored
		if !ok {
			res.OK, res.Key = false, k
			return res
		}
	}
	return res
}

type entry struct {
	op         int
	isCall     bool
	at         Time
	match      *entry
	prev, next *entry
}

// step applies op to a register state ("" means absent, "=v" holds v).
func step(state string, op Op) (bool, string) {
	if op.Input.Kind == OpPut {
		return true, "=" + op.Input.Value
	}
	if op.Output == Absent {
		return state == "", state
	}
	return state == "="+op.Output, state
}

func checkRegister(ops []Op) (bool, int) {
	if len(ops) == 0 {
		return true, 0
	}
	evs := make([]*entry, 0, 2*len(ops))
	for i, op := range ops {
		c := &entry{op: i, isCall: true, at: op.Call}
		r := &entry{op: i, at: op.Return}
		c.match = r
		evs = append(evs, c, r)
	}
	// Calls sort before returns at equal times: such ops are concurrent.
	sort.SliceStable(evs, func(i, j int) bool {
		a, b := evs[i], evs[j]
		if a.at != b.at {
			return a.at < b.at
		}
		if a.isCall != b.isCall {
			return a.isCall
		}
		return a.op < b.op
	})
	head := &entry{}
	cur := head
	for _, e := range evs {
		cur.next, e.prev = e, cur
		cur = e
	}

	type frame struct {
		e     *entry
		state string
	}
	var stack []frame
	lin := make([]uint64, (len(ops)+63)/64)
	cache := map[string]bool{}
	state := ""
	explored := 0
	e := head.next
	for head.next != nil {
		if e.isCall {
			ok, next := step(state, ops[e.op])
			if ok {
				lin[e.op/64] |= 1 << (e.op % 64)
				key := cacheKey(lin, next)
				if !cache[key] {
					cache[key] = true
					explored++
					stack = append(stack, frame{e, state})
					state = next
					lift(e)
					e = head.next
					continue
				}
				lin[e.op/64] &^= 1 << (e.op % 64)
			}
			e = e.next
			continue
		}
		// A return whose call is still in the list: backtrack.
		if len(stack) == 0 {
			return false, explored
		}
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		state = top.state
		lin[top.e.op/64] &^= 1 << (top.e.op % 64)
		unlift(top.e)
		e = top.e.next
	}
	return true, explored
}

func lift(e *entry) {
	e.prev.next = e.next
	if e.next != nil {
		e.next.prev = e.prev
	}
	m := e.match
	m.prev.next = m.next
	if m.next != nil {
		m.next.prev = m.prev
	}
}

func unlift(e *entry) {
	m := e.match
	m.prev.next = m
	if m.next != nil {
		m.next.prev = m
	}
	e.prev.next = e
	if e.next != nil {
		e.next.prev = e
	}
}

func cacheKey(lin []uint64, state string) string {
	var b strings.Builder
	for _, w := range lin {
		b.WriteString(strconv.FormatUint(w, 16))
		b.WriteByte('.')
	}
	b.WriteString(state)
	return b.String()
}
