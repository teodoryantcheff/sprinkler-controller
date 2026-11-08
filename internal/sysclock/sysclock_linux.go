//go:build linux

package sysclock

import "golang.org/x/sys/unix"

// synced reads the kernel NTP state read-only (modes = 0), the same test
// timedatectl uses for "System clock synchronized".
func synced() bool {
	var tx unix.Timex
	state, err := unix.Adjtimex(&tx)
	if err != nil {
		return true // can't tell: don't block watering on it
	}
	return state != unix.TIME_ERROR && tx.Status&unix.STA_UNSYNC == 0
}
