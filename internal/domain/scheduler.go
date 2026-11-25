package domain

import (
	"container/heap"
	"fmt"
	"hash/fnv"
	"sort"
)

type eventKind uint8

const (
	evStart eventKind = iota
	evDeliver
	evTimer
	evRestart
	evFaultTick
	evCall
)

type event struct {
	at    Time
	seq   uint64
	kind  eventKind
	node  NodeID
	epoch uint64
	tag   uint64
	msg   Message
	dup   bool
	label string
	fn    func(Env, Process)
}

type eventQueue []*event

func (q eventQueue) Len() int { return len(q) }
func (q eventQueue) Less(i, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	return q[i].seq < q[j].seq
}
func (q eventQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *eventQueue) Push(x any)   { *q = append(*q, x.(*event)) }
func (q *eventQueue) Pop() any {
	old := *q
	e := old[len(old)-1]
	*q = old[:len(old)-1]
	return e
}

// SimConfig configures a simulation. Transport defaults to a FaultyNetwork
// built from Plan.
type SimConfig struct {
	Seed      uint64
	Horizon   Time
	Plan      FaultPlan
	Transport Transport
}

// Stats counts what happened during a run.
type Stats struct {
	Scheduled, Dispatched                           int
	Sent, Delivered, Dropped, Duplicated, Reordered int
	LostToCrash, Crashes, Restarts, Timers          int
}

type node struct {
	id        NodeID
	factory   func() Process
	proc      Process
	storage   Storage
	crashable bool
	up        bool
	epoch     uint64
}

// Sim is the single-threaded deterministic event loop.
type Sim struct {
	cfg       SimConfig
	clock     Clock
	rng       *Rand
	queue     eventQueue
	seq       uint64
	msgID     uint64
	nodes     map[NodeID]*node
	order     []NodeID
	trace     Trace
	stats     Stats
	cur       uint64
	started   bool
	transport Transport
}

// NewSim validates cfg and returns an empty simulation.
func NewSim(cfg SimConfig) (*Sim, error) {
	if err := cfg.Plan.Validate(); err != nil {
		return nil, err
	}
	if cfg.Horizon <= 0 {
		return nil, fmt.Errorf("%w: horizon must be > 0", ErrInvalidConfig)
	}
	t := cfg.Transport
	if t == nil {
		t = NewFaultyNetwork(cfg.Plan)
	}
	return &Sim{cfg: cfg, rng: NewRand(cfg.Seed), nodes: map[NodeID]*node{}, transport: t}, nil
}

// AddNode registers a process. factory is called at start and after every
// restart; storage survives crashes. Only crashable nodes receive crashes.
func (s *Sim) AddNode(id NodeID, factory func() Process, storage Storage, crashable bool) error {
	if !IsToken(string(id)) {
		return fmt.Errorf("%w: node id %q", ErrInvalidConfig, id)
	}
	if _, ok := s.nodes[id]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicateNode, id)
	}
	if s.started {
		return fmt.Errorf("%w: cannot add nodes after Run", ErrInvalidConfig)
	}
	s.nodes[id] = &node{id: id, factory: factory, storage: storage, crashable: crashable}
	s.order = append(s.order, id)
	sort.Slice(s.order, func(i, j int) bool { return s.order[i] < s.order[j] })
	return nil
}

// Call schedules fn to run against node id's live process at time at. If the
// node is down at that time the call is skipped and recorded.
func (s *Sim) Call(at Time, id NodeID, label string, fn func(Env, Process)) error {
	if _, ok := s.nodes[id]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownNode, id)
	}
	s.push(&event{at: at, kind: evCall, node: id, label: label, fn: fn})
	return nil
}

// Now returns the current virtual time.
func (s *Sim) Now() Time { return s.clock.Now() }

// Note appends a free-form line to the trace at the current time.
func (s *Sim) Note(id NodeID, detail string) { s.record(KindNote, id, detail) }

// Trace returns the trace recorded so far.
func (s *Sim) Trace() Trace { return s.trace }

// Stats returns the counters recorded so far.
func (s *Sim) Stats() Stats { return s.stats }

// PendingWithinHorizon counts queued events with at <= horizon; zero after
// a completed Run (invariant I3).
func (s *Sim) PendingWithinHorizon() int {
	n := 0
	for _, e := range s.queue {
		if e.at <= s.cfg.Horizon {
			n++
		}
	}
	return n
}

// Run starts every node at time 0 (in id order) and dispatches events until
// the queue is empty or the next event lies beyond the horizon.
func (s *Sim) Run() error {
	if s.started {
		return fmt.Errorf("%w: Run called twice", ErrInvalidConfig)
	}
	s.started = true
	for _, id := range s.order {
		s.push(&event{at: 0, kind: evStart, node: id})
	}
	if s.cfg.Plan.Crash > 0 && s.cfg.Plan.MaxCrashed > 0 {
		s.push(&event{at: Time(s.cfg.Plan.CrashEvery), kind: evFaultTick})
	}
	for s.queue.Len() > 0 {
		e := heap.Pop(&s.queue).(*event)
		if e.at > s.cfg.Horizon {
			heap.Push(&s.queue, e)
			break
		}
		if err := s.clock.AdvanceTo(e.at); err != nil {
			return err
		}
		s.cur = e.seq
		s.stats.Dispatched++
		s.dispatch(e)
	}
	return nil
}

func (s *Sim) push(e *event) {
	if e.at < s.clock.Now() {
		e.at = s.clock.Now()
	}
	s.seq++
	e.seq = s.seq
	s.stats.Scheduled++
	heap.Push(&s.queue, e)
}

func (s *Sim) record(kind string, id NodeID, detail string) {
	s.trace = append(s.trace, TraceEvent{At: s.clock.Now(), Seq: s.cur, Kind: kind, Node: id, Detail: detail})
}

func (s *Sim) dispatch(e *event) {
	if e.kind == evFaultTick {
		s.faultTick()
		return
	}
	n := s.nodes[e.node]
	env := &nodeEnv{s: s, n: n}
	switch e.kind {
	case evStart, evRestart:
		n.up, n.epoch, n.proc = true, n.epoch+1, n.factory()
		kind := KindStart
		if e.kind == evRestart {
			kind = KindRestart
			s.stats.Restarts++
		}
		s.record(kind, n.id, fmt.Sprintf("epoch=%d", n.epoch))
		n.proc.Start(env)
	case evDeliver:
		if !n.up {
			s.stats.LostToCrash++
			s.record(KindLost, n.id, fmt.Sprintf("id=%d from=%s", e.msg.ID, e.msg.From))
			return
		}
		s.stats.Delivered++
		s.record(KindDeliver, n.id, fmt.Sprintf("id=%d from=%s dup=%t h=%016x", e.msg.ID, e.msg.From, e.dup, hashBytes(e.msg.Payload)))
		n.proc.Receive(env, e.msg.From, e.msg.Payload)
	case evTimer:
		if !n.up || e.epoch != n.epoch {
			return // invariant I6: timers of old incarnations never fire
		}
		s.stats.Timers++
		s.record(KindTimer, n.id, fmt.Sprintf("tag=%d", e.tag))
		n.proc.Timer(env, e.tag)
	case evCall:
		if !n.up {
			s.record(KindCall, n.id, "skipped "+e.label)
			return
		}
		s.record(KindCall, n.id, e.label)
		e.fn(env, n.proc)
	}
}

func (s *Sim) faultTick() {
	down := 0
	for _, id := range s.order {
		if !s.nodes[id].up {
			down++
		}
	}
	p := s.cfg.Plan
	for _, id := range s.order {
		n := s.nodes[id]
		if !n.crashable || !n.up || down >= p.MaxCrashed || !s.rng.Chance(p.Crash) {
			continue
		}
		n.up, n.proc = false, nil
		down++
		s.stats.Crashes++
		back := s.clock.Now() + Time(s.rng.Range(int64(p.RestartMin), int64(p.RestartMax)))
		s.record(KindCrash, id, fmt.Sprintf("restart-at=%d", back))
		s.push(&event{at: back, kind: evRestart, node: id})
	}
	s.push(&event{at: s.clock.Now() + Time(p.CrashEvery), kind: evFaultTick})
}

func hashBytes(b []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return h.Sum64()
}

type nodeEnv struct {
	s *Sim
	n *node
}

func (e *nodeEnv) Self() NodeID     { return e.n.id }
func (e *nodeEnv) Now() Time        { return e.s.clock.Now() }
func (e *nodeEnv) Storage() Storage { return e.n.storage }
func (e *nodeEnv) Rand() *Rand      { return e.s.rng }

func (e *nodeEnv) After(d Duration, tag uint64) {
	if d < 0 {
		d = 0
	}
	e.s.push(&event{at: e.s.clock.Now() + Time(d), kind: evTimer, node: e.n.id, epoch: e.n.epoch, tag: tag})
}

func (e *nodeEnv) Send(to NodeID, payload []byte) {
	s := e.s
	s.msgID++
	s.stats.Sent++
	m := Message{ID: s.msgID, From: e.n.id, To: to, Payload: append([]byte(nil), payload...), SentAt: s.clock.Now()}
	if _, ok := s.nodes[to]; !ok {
		s.stats.Dropped++
		s.record(KindDrop, e.n.id, fmt.Sprintf("id=%d to=%s unknown", m.ID, to))
		return
	}
	ds := s.transport.Route(m, s.clock.Now(), s.rng)
	if len(ds) == 0 {
		s.stats.Dropped++
		s.record(KindDrop, e.n.id, fmt.Sprintf("id=%d to=%s", m.ID, to))
		return
	}
	s.record(KindSend, e.n.id, fmt.Sprintf("id=%d to=%s copies=%d", m.ID, to, len(ds)))
	for _, d := range ds {
		if d.Duplicate {
			s.stats.Duplicated++
		}
		if d.Reordered {
			s.stats.Reordered++
		}
		s.push(&event{at: d.At, kind: evDeliver, node: to, msg: m, dup: d.Duplicate})
	}
}
