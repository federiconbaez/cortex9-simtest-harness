// Package kv is the reference system under test: a primary-backup KV store
// written against the domain ports. n0 is the fixed primary; there is no
// failover. Variant LazyFlush plants a durability bug.
package kv

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/federiconbaez/cortex9-simtest-harness/internal/domain"
)

// Variant selects the server implementation.
type Variant int

// Variants of the KV server.
const (
	// Correct persists each put (value + client dedupe seq) atomically before
	// replicating and acknowledging.
	Correct Variant = iota
	// LazyFlush acknowledges puts from memory and flushes to storage on a
	// timer: a primary crash loses acknowledged writes (planted bug).
	LazyFlush
)

// Primary is the fixed primary node.
const Primary domain.NodeID = "n0"

const (
	tagRetry     uint64 = 1
	tagFlush     uint64 = 2
	retryEvery          = 20
	flushEvery          = 60
	clientResend        = 30
)

// System implements domain.SUT.
type System struct {
	variant  Variant
	replicas int
}

// New builds a system with the given variant and number of servers (>= 1).
func New(v Variant, replicas int) *System {
	if replicas < 1 {
		replicas = 1
	}
	return &System{variant: v, replicas: replicas}
}

// ByName resolves "kv" and "kv-lazy".
func ByName(name string, replicas int) (*System, error) {
	switch name {
	case "kv":
		return New(Correct, replicas), nil
	case "kv-lazy":
		return New(LazyFlush, replicas), nil
	}
	return nil, fmt.Errorf("%w: unknown sut %q (want kv or kv-lazy)", domain.ErrInvalidConfig, name)
}

// Name implements domain.SUT.
func (s *System) Name() string {
	if s.variant == LazyFlush {
		return "kv-lazy"
	}
	return "kv"
}

// Servers implements domain.SUT.
func (s *System) Servers() []domain.NodeID {
	ids := make([]domain.NodeID, s.replicas)
	for i := range ids {
		ids[i] = domain.NodeID("n" + strconv.Itoa(i))
	}
	return ids
}

// NewServer implements domain.SUT. It is called again after each restart,
// so all in-memory state is lost on crash.
func (s *System) NewServer(id domain.NodeID) domain.Process {
	srv := &server{variant: s.variant, primary: id == Primary, pending: map[uint64]*rep{}, cache: map[string]string{}, cacheSeq: map[domain.NodeID]uint64{}}
	if srv.primary {
		srv.backups = s.Servers()[1:]
	}
	return srv
}

// NewClient implements domain.SUT.
func (s *System) NewClient(id domain.NodeID) domain.Client { return &client{} }

type rep struct {
	client domain.NodeID
	seq    uint64
	key    string
	value  string
	acked  map[domain.NodeID]bool
}

type server struct {
	variant  Variant
	primary  bool
	backups  []domain.NodeID
	ver      uint64
	pending  map[uint64]*rep
	cache    map[string]string        // LazyFlush: unflushed values
	cacheSeq map[domain.NodeID]uint64 // LazyFlush: unflushed dedupe seqs
	dirty    bool
}

func send(env domain.Env, to domain.NodeID, f ...string) {
	env.Send(to, []byte(strings.Join(f, " ")))
}

func (s *server) Start(env domain.Env) {
	if !s.primary {
		return
	}
	if v, ok := env.Storage().Get("ver"); ok {
		s.ver, _ = strconv.ParseUint(v, 10, 64)
	}
	env.After(retryEvery, tagRetry)
	if s.variant == LazyFlush {
		env.After(flushEvery, tagFlush)
	}
}

func (s *server) lastSeq(env domain.Env, c domain.NodeID) uint64 {
	if v, ok := s.cacheSeq[c]; ok {
		return v
	}
	v, _ := env.Storage().Get("c/" + string(c))
	n, _ := strconv.ParseUint(v, 10, 64)
	return n
}

func (s *server) read(env domain.Env, key string) string {
	if v, ok := s.cache[key]; ok {
		return v
	}
	if v, ok := env.Storage().Get("v/" + key); ok {
		return v
	}
	return domain.Absent
}

func (s *server) Receive(env domain.Env, from domain.NodeID, payload []byte) {
	f := strings.Fields(string(payload))
	if len(f) < 2 {
		return
	}
	num, err := strconv.ParseUint(f[1], 10, 64)
	if err != nil {
		return
	}
	switch {
	case !s.primary && f[0] == "rep" && len(f) == 4:
		s.applyReplica(env, num, f[2], f[3])
		send(env, from, "ack", f[1])
	case s.primary && f[0] == "get" && len(f) == 3:
		send(env, from, "ok", f[1], s.read(env, f[2]))
	case s.primary && f[0] == "put" && len(f) == 4:
		s.put(env, from, num, f[2], f[3])
	case s.primary && f[0] == "ack":
		s.ack(env, from, num)
	}
}

func (s *server) applyReplica(env domain.Env, ver uint64, key, value string) {
	cur, _ := env.Storage().Get("kv/" + key)
	if n, _ := strconv.ParseUint(cur, 10, 64); ver <= n {
		return
	}
	env.Storage().Apply([]domain.KV{{Key: "v/" + key, Value: value}, {Key: "kv/" + key, Value: strconv.FormatUint(ver, 10)}})
}

func (s *server) put(env domain.Env, c domain.NodeID, seq uint64, key, value string) {
	if seq <= s.lastSeq(env, c) {
		for _, r := range s.pending {
			if r.client == c && r.seq == seq {
				return // still replicating; the ack will follow
			}
		}
		send(env, c, "ok", strconv.FormatUint(seq, 10), domain.Absent)
		return
	}
	s.ver++
	r := &rep{client: c, seq: seq, key: key, value: value, acked: map[domain.NodeID]bool{}}
	if s.variant == LazyFlush {
		s.cache[key], s.cacheSeq[c], s.dirty = value, seq, true
		send(env, c, "ok", strconv.FormatUint(seq, 10), domain.Absent) // BUG: ack before durable
		r.client = ""
	} else {
		env.Storage().Apply([]domain.KV{
			{Key: "v/" + key, Value: value},
			{Key: "c/" + string(c), Value: strconv.FormatUint(seq, 10)},
			{Key: "ver", Value: strconv.FormatUint(s.ver, 10)},
		})
	}
	if len(s.backups) == 0 {
		s.finish(env, r)
		return
	}
	s.pending[s.ver] = r
	for _, b := range s.backups {
		send(env, b, "rep", strconv.FormatUint(s.ver, 10), key, value)
	}
}

func (s *server) ack(env domain.Env, from domain.NodeID, ver uint64) {
	r, ok := s.pending[ver]
	if !ok {
		return
	}
	r.acked[from] = true
	if len(r.acked) == len(s.backups) {
		delete(s.pending, ver)
		s.finish(env, r)
	}
}

func (s *server) finish(env domain.Env, r *rep) {
	if r.client != "" {
		send(env, r.client, "ok", strconv.FormatUint(r.seq, 10), domain.Absent)
	}
}

func (s *server) Timer(env domain.Env, tag uint64) {
	switch tag {
	case tagRetry:
		vers := make([]uint64, 0, len(s.pending))
		for v := range s.pending {
			vers = append(vers, v)
		}
		sort.Slice(vers, func(i, j int) bool { return vers[i] < vers[j] })
		for _, v := range vers {
			r := s.pending[v]
			for _, b := range s.backups {
				if !r.acked[b] {
					send(env, b, "rep", strconv.FormatUint(v, 10), r.key, r.value)
				}
			}
		}
		env.After(retryEvery, tagRetry)
	case tagFlush:
		if s.dirty {
			s.flush(env)
		}
		env.After(flushEvery, tagFlush)
	}
}

func (s *server) flush(env domain.Env) {
	batch := make([]domain.KV, 0, len(s.cache)+len(s.cacheSeq)+1)
	for k, v := range s.cache {
		batch = append(batch, domain.KV{Key: "v/" + k, Value: v})
	}
	for c, n := range s.cacheSeq {
		batch = append(batch, domain.KV{Key: "c/" + string(c), Value: strconv.FormatUint(n, 10)})
	}
	sort.Slice(batch, func(i, j int) bool { return batch[i].Key < batch[j].Key })
	batch = append(batch, domain.KV{Key: "ver", Value: strconv.FormatUint(s.ver, 10)})
	env.Storage().Apply(batch)
	s.cache, s.cacheSeq, s.dirty = map[string]string{}, map[domain.NodeID]uint64{}, false
}

type call struct {
	seq  uint64
	msg  string
	done func(string)
}

type client struct {
	seq uint64
	cur *call
}

func (c *client) Start(domain.Env) {}

// Invoke implements domain.Client. The workload never overlaps operations
// of one client; a second Invoke while busy is ignored.
func (c *client) Invoke(env domain.Env, in domain.Input, done func(string)) {
	if c.cur != nil {
		return
	}
	c.seq++
	s := strconv.FormatUint(c.seq, 10)
	msg := "get " + s + " " + in.Key
	if in.Kind == domain.OpPut {
		msg = "put " + s + " " + in.Key + " " + in.Value
	}
	c.cur = &call{seq: c.seq, msg: msg, done: done}
	env.Send(Primary, []byte(msg))
	env.After(clientResend, c.seq)
}

func (c *client) Receive(_ domain.Env, _ domain.NodeID, payload []byte) {
	f := strings.Fields(string(payload))
	if len(f) != 3 || f[0] != "ok" || c.cur == nil {
		return
	}
	if seq, err := strconv.ParseUint(f[1], 10, 64); err != nil || seq != c.cur.seq {
		return
	}
	done := c.cur.done
	c.cur = nil
	done(f[2])
}

func (c *client) Timer(env domain.Env, tag uint64) {
	if c.cur == nil || tag != c.cur.seq {
		return
	}
	env.Send(Primary, []byte(c.cur.msg))
	env.After(clientResend, tag)
}

var _ domain.SUT = (*System)(nil)
