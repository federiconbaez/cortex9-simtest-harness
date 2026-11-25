package domain

// NodeID names a simulated process (server or client). It must be a token
// without whitespace.
type NodeID string

// KV is one key/value pair of an atomic storage batch.
type KV struct{ Key, Value string }

// Storage is the durable storage port. Everything applied survives a crash
// of the owning process; process memory does not.
type Storage interface {
	Get(key string) (string, bool)
	// Apply writes the whole batch atomically.
	Apply(batch []KV)
}

// Env is what the simulator offers a process while it handles an event.
// It is only valid during that callback.
type Env interface {
	Self() NodeID
	Now() Time
	Send(to NodeID, payload []byte)
	After(d Duration, tag uint64)
	Storage() Storage
	Rand() *Rand
}

// Process is the node port: a cooperative state machine driven by the
// simulator. It must not block, sleep or start goroutines.
type Process interface {
	Start(env Env)
	Receive(env Env, from NodeID, payload []byte)
	Timer(env Env, tag uint64)
}

// Client is a process that also accepts operations from the workload.
// It reports completion exactly once by calling done with the output.
type Client interface {
	Process
	Invoke(env Env, in Input, done func(output string))
}

// SUT is the system-under-test port: it names its servers and builds fresh
// server and client processes (a restart calls NewServer again).
type SUT interface {
	Name() string
	Servers() []NodeID
	NewServer(id NodeID) Process
	NewClient(id NodeID) Client
}
