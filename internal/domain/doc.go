// Package domain is the pure core of the simulation harness: virtual clock,
// seeded RNG, single-threaded scheduler, simulated faulty network, fault
// plans, traces, histories and the linearizability checker.
//
// It performs no I/O, never reads the wall clock and starts no goroutines.
package domain
