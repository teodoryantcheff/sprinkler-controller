//go:build !linux

package sysclock

func synced() bool { return true }
