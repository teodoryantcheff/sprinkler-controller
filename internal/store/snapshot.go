package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sprinkler/internal/fsutil"
)

// Snapshots are daily, checked copies of the database, so a file damaged
// by a power loss can be replaced by yesterday's. They are named
// sprinkler-YYYY-MM-DD.db after the local day they were taken.

const snapPrefix, snapSuffix = "sprinkler-", ".db"

// SnapshotName is the file name of the snapshot for a local day.
func SnapshotName(day string) string { return snapPrefix + day + snapSuffix }

// Snapshots lists the snapshot files in dir, newest first.
func Snapshots(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if e.Type().IsRegular() && strings.HasPrefix(n, snapPrefix) && strings.HasSuffix(n, snapSuffix) {
			out = append(out, filepath.Join(dir, n))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

// LatestSnapshot returns the newest snapshot and when it was written
// ("" when there is none).
func LatestSnapshot(dir string) (string, time.Time, error) {
	snaps, err := Snapshots(dir)
	if err != nil || len(snaps) == 0 {
		return "", time.Time{}, err
	}
	fi, err := os.Stat(snaps[0])
	if err != nil {
		return "", time.Time{}, err
	}
	return snaps[0], fi.ModTime(), nil
}

// Snapshot writes a consistent copy of the database to dir as the
// snapshot for day, checks it, and keeps the newest keep snapshots. The
// copy is made from a separate read-only connection, so the controller's
// writes are not held up meanwhile (WAL readers don't block the writer).
func (s *Store) Snapshot(dir, day string, keep int) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	final := filepath.Join(dir, SnapshotName(day))
	tmp := final + ".tmp"
	_ = os.Remove(tmp)
	ro, err := sql.Open("sqlite", "file:"+s.path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return "", err
	}
	_, err = ro.Exec("VACUUM INTO ?", tmp)
	ro.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("snapshot: %w", err)
	}
	if err := checkFile(tmp); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("snapshot check: %w", err)
	}
	if err := fsutil.SyncFile(tmp); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	snaps, err := Snapshots(dir)
	if err != nil {
		return final, err
	}
	for i, p := range snaps {
		if i >= keep {
			_ = os.Remove(p)
		}
	}
	return final, fsutil.SyncDir(dir)
}

// checkFile runs the integrity check on a database file without writing.
func checkFile(path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	return (&Store{DB: db, path: path}).QuickCheck()
}

// RestoreSnapshot moves the damaged database at path (and its -wal and
// -shm files) aside as <path>.corrupt-<time>, and copies the newest good
// snapshot from dir in its place. It returns the snapshot used.
func RestoreSnapshot(path, dir string, now time.Time) (string, error) {
	snaps, err := Snapshots(dir)
	if err != nil {
		return "", err
	}
	var src string
	for _, p := range snaps {
		if checkFile(p) == nil {
			src = p
			break
		}
	}
	if src == "" {
		return "", fmt.Errorf("no usable snapshot in %s", dir)
	}
	aside := path + ".corrupt-" + now.UTC().Format("20060102T150405Z")
	for _, ext := range []string{"", "-wal", "-shm"} {
		if err := os.Rename(path+ext, aside+ext); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if err := copyFile(src, path); err != nil {
		return "", err
	}
	return src, fsutil.SyncDir(filepath.Dir(path))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".restore-tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
