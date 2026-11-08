// Package sysclock tells whether the system clock has been set from a
// time source (NTP). The Pi has no hardware clock: until NTP answers,
// the time is whatever fake-hwclock saved at the last shutdown.
package sysclock

// Synced reports whether the kernel considers the clock synchronised.
// Where this can't be known it returns true.
func Synced() bool { return synced() }
