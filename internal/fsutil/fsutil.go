// Package fsutil writes files so that a power loss leaves either the old
// or the new content on disk, never a torn or empty file.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to a temporary file next to path, syncs it,
// renames it over path and syncs the directory.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return SyncDir(dir)
}

// SyncFile flushes an existing file to disk.
func SyncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// SyncDir flushes a directory entry (after create, rename or remove).
func SyncDir(dir string) error {
	return SyncFile(dir)
}
