package domain

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
)

// Trace kinds recorded by the simulator.
const (
	KindStart   = "start"
	KindRestart = "restart"
	KindDeliver = "deliver"
	KindLost    = "lost"
	KindTimer   = "timer"
	KindCrash   = "crash"
	KindCall    = "call"
	KindSend    = "send"
	KindDrop    = "drop"
	KindNote    = "note"
)

var traceKinds = map[string]bool{
	KindStart: true, KindRestart: true, KindDeliver: true, KindLost: true, KindTimer: true,
	KindCrash: true, KindCall: true, KindSend: true, KindDrop: true, KindNote: true,
}

// TraceEvent is one line of the canonical trace. Seq is the scheduler
// sequence number of the event being dispatched when the line was written.
type TraceEvent struct {
	At     Time
	Seq    uint64
	Kind   string
	Node   NodeID
	Detail string
}

// Trace is the ordered record of a run. Same seed and config produce the
// same trace, byte for byte.
type Trace []TraceEvent

// Format renders the trace as "<at> <seq> <kind> <node> <detail>" lines.
// An empty detail is written as "-".
func (t Trace) Format() string {
	var b strings.Builder
	for _, e := range t {
		d := e.Detail
		if d == "" {
			d = "-"
		}
		fmt.Fprintf(&b, "%d %d %s %s %s\n", e.At, e.Seq, e.Kind, e.Node, d)
	}
	return b.String()
}

// Digest is the FNV-64a hash of Format, handy to compare runs.
func (t Trace) Digest() uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(t.Format()))
	return h.Sum64()
}

// ParseTrace parses the output of Format. Blank lines are ignored.
func ParseTrace(s string) (Trace, error) {
	var t Trace
	for i, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.SplitN(line, " ", 5)
		if len(f) != 5 {
			return nil, fmt.Errorf("%w: trace line %d: want 5 fields", ErrParse, i+1)
		}
		at, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil || at < 0 {
			return nil, fmt.Errorf("%w: trace line %d: bad time %q", ErrParse, i+1, f[0])
		}
		seq, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: trace line %d: bad seq %q", ErrParse, i+1, f[1])
		}
		if !traceKinds[f[2]] {
			return nil, fmt.Errorf("%w: trace line %d: unknown kind %q", ErrParse, i+1, f[2])
		}
		if !IsToken(f[3]) {
			return nil, fmt.Errorf("%w: trace line %d: bad node %q", ErrParse, i+1, f[3])
		}
		d := f[4]
		if d == "" || strings.ContainsAny(d, "\r\t") {
			return nil, fmt.Errorf("%w: trace line %d: bad detail", ErrParse, i+1)
		}
		if d == "-" {
			d = ""
		}
		t = append(t, TraceEvent{At: Time(at), Seq: seq, Kind: f[2], Node: NodeID(f[3]), Detail: d})
	}
	return t, nil
}

// IsToken reports whether s is non-empty printable ASCII without spaces.
func IsToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] > '~' {
			return false
		}
	}
	return true
}
