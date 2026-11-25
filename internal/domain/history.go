package domain

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// OpKind is the kind of a KV operation.
type OpKind uint8

// Supported operations of the KV model.
const (
	OpPut OpKind = iota + 1
	OpGet
)

// Absent is the output of a get on a key that was never written.
const Absent = "-"

const unknown = "?"

func (k OpKind) String() string {
	switch k {
	case OpPut:
		return "put"
	case OpGet:
		return "get"
	}
	return "invalid"
}

// Input is an operation as issued by the workload.
type Input struct {
	Kind  OpKind
	Key   string
	Value string // only for put
}

// Op is one entry of a history: the call, its return (or Pending) and the
// observed output (the read value for get, empty for put).
type Op struct {
	ID      int
	Client  NodeID
	Input   Input
	Output  string
	Call    Time
	Return  Time
	Pending bool
}

// History is the set of operations recorded during a run.
type History []Op

// Validate checks tokens and time ordering.
func (h History) Validate() error {
	seen := map[int]bool{}
	for _, op := range h {
		bad := func(why string) error { return fmt.Errorf("%w: op %d: %s", ErrInvalidHistory, op.ID, why) }
		switch {
		case seen[op.ID]:
			return bad("duplicate id")
		case !IsToken(string(op.Client)):
			return bad("bad client")
		case !IsToken(op.Input.Key):
			return bad("bad key")
		case op.Call < 0:
			return bad("negative call time")
		case !op.Pending && op.Return < op.Call:
			return bad("return before call")
		}
		seen[op.ID] = true
		switch op.Input.Kind {
		case OpPut:
			if !IsToken(op.Input.Value) || op.Input.Value == Absent || op.Input.Value == unknown {
				return bad("bad put value")
			}
		case OpGet:
			if !op.Pending && (!IsToken(op.Output) || op.Output == unknown) {
				return bad("bad get output")
			}
		default:
			return bad("unknown kind")
		}
	}
	return nil
}

// Format renders one line per op:
// "<id> <client> <call> <return|pending> put <key> <value>" or
// "<id> <client> <call> <return|pending> get <key> <output|?>".
func (h History) Format() string {
	var b strings.Builder
	for _, op := range h {
		ret := "pending"
		if !op.Pending {
			ret = strconv.FormatInt(int64(op.Return), 10)
		}
		arg := op.Input.Value
		if op.Input.Kind == OpGet {
			arg = op.Output
			if op.Pending {
				arg = unknown
			}
		}
		fmt.Fprintf(&b, "%d %s %d %s %s %s %s\n", op.ID, op.Client, op.Call, ret, op.Input.Kind, op.Input.Key, arg)
	}
	return b.String()
}

// ParseHistory parses the output of Format and validates the result.
func ParseHistory(s string) (History, error) {
	var h History
	for i, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 7 {
			return nil, fmt.Errorf("%w: history line %d: want 7 fields", ErrParse, i+1)
		}
		id, err1 := strconv.Atoi(f[0])
		call, err2 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("%w: history line %d: bad number", ErrParse, i+1)
		}
		op := Op{ID: id, Client: NodeID(f[1]), Call: Time(call), Input: Input{Key: f[5]}}
		if f[3] == "pending" {
			op.Pending, op.Return = true, Never
		} else {
			r, err := strconv.ParseInt(f[3], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: history line %d: bad return", ErrParse, i+1)
			}
			op.Return = Time(r)
		}
		switch f[4] {
		case "put":
			op.Input.Kind, op.Input.Value = OpPut, f[6]
		case "get":
			op.Input.Kind = OpGet
			if !op.Pending {
				op.Output = f[6]
			} else if f[6] != unknown {
				return nil, fmt.Errorf("%w: history line %d: pending get must have output ?", ErrParse, i+1)
			}
		default:
			return nil, fmt.Errorf("%w: history line %d: unknown kind %q", ErrParse, i+1, f[4])
		}
		h = append(h, op)
	}
	if err := h.Validate(); err != nil {
		return nil, err
	}
	return h, nil
}

// Keys returns the distinct keys of h in sorted order.
func (h History) Keys() []string {
	set := map[string]bool{}
	for _, op := range h {
		set[op.Input.Key] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
