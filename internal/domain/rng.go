package domain

// Rand is a seeded SplitMix64 generator. Its sequence depends only on the
// seed, not on the Go version, which keeps traces reproducible.
type Rand struct{ state uint64 }

// NewRand returns a generator seeded with seed.
func NewRand(seed uint64) *Rand { return &Rand{state: seed} }

// Uint64 returns the next 64 pseudo-random bits.
func (r *Rand) Uint64() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Intn returns a value in [0, n). It returns 0 when n <= 0.
// Modulo bias is negligible for the small n used by the harness.
func (r *Rand) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.Uint64() % uint64(n))
}

// Range returns a value in [lo, hi]. It returns lo when hi <= lo.
func (r *Rand) Range(lo, hi int64) int64 {
	if hi <= lo {
		return lo
	}
	return lo + int64(r.Uint64()%uint64(hi-lo+1))
}

// Chance reports true with probability perMille/1000.
func (r *Rand) Chance(perMille int) bool {
	if perMille <= 0 {
		return false
	}
	return r.Intn(1000) < perMille
}

// Fork derives an independent generator from the next value of r.
func (r *Rand) Fork() *Rand { return NewRand(r.Uint64()) }
