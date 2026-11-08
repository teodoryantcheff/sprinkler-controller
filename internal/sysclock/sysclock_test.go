package sysclock

import "testing"

func TestSynced(t *testing.T) {
	t.Logf("clock synced: %v", Synced()) // must not panic or block
}
