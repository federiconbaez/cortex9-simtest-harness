package domain

// Message is one payload in flight between two simulated processes.
type Message struct {
	ID       uint64
	From, To NodeID
	Payload  []byte
	SentAt   Time
}

// Delivery is one scheduled arrival of a message. A message can produce
// zero (dropped), one or two (duplicated) deliveries.
type Delivery struct {
	At        Time
	Duplicate bool
	Reordered bool
}

// Transport is the network port used by the scheduler to decide when (and
// whether) a message arrives. Implementations must be deterministic given rng.
type Transport interface {
	Route(m Message, now Time, rng *Rand) []Delivery
}

type link struct{ from, to NodeID }

// FaultyNetwork is the simulated network. Without reorder it keeps FIFO
// order per link by never scheduling a delivery before the previous one.
type FaultyNetwork struct {
	plan FaultPlan
	last map[link]Time
}

// NewFaultyNetwork builds a network that injects the faults of plan.
func NewFaultyNetwork(plan FaultPlan) *FaultyNetwork {
	return &FaultyNetwork{plan: plan, last: map[link]Time{}}
}

// Route implements Transport.
func (n *FaultyNetwork) Route(m Message, now Time, rng *Rand) []Delivery {
	if rng.Chance(n.plan.Drop) {
		return nil
	}
	out := []Delivery{n.one(m, now, rng, false)}
	if rng.Chance(n.plan.Dup) {
		out = append(out, n.one(m, now, rng, true))
	}
	return out
}

func (n *FaultyNetwork) one(m Message, now Time, rng *Rand, dup bool) Delivery {
	l := link{m.From, m.To}
	at := now + Time(rng.Range(int64(n.plan.MinDelay), int64(n.plan.MaxDelay)))
	if rng.Chance(n.plan.Reorder) {
		at += Time(rng.Range(1, int64(n.plan.ReorderExtra)))
		return Delivery{At: at, Duplicate: dup, Reordered: true}
	}
	if prev, ok := n.last[l]; ok && at < prev {
		at = prev
	}
	n.last[l] = at
	return Delivery{At: at, Duplicate: dup}
}
