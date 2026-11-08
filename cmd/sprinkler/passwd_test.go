package main

import (
	"path/filepath"
	"testing"

	"sprinkler/internal/api"
	"sprinkler/internal/store"
)

func TestResetPassword(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetRawSetting("session_secret", "deadbeef")
	if err := resetPassword(st, "admin", "short", true); err == nil {
		t.Error("too-short password accepted")
	}
	if err := resetPassword(st, "admin", "new pass7", true); err != nil {
		t.Fatal(err)
	}
	u, err := st.UserByName("admin")
	if err != nil || !api.CheckPassword(u.PasswordHash, "new pass7") {
		t.Fatal("new password does not verify")
	}
	if v, _ := st.RawSetting("session_secret"); v != "" {
		t.Error("sessions should be logged out (secret cleared)")
	}
}
