// Package fsutil holds the small file operations the persistent stores share.
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// WriteFileAtomic replaces path with data so that a reader, or a crash, sees
// either the old file or the new one and never a truncated mix. The data goes to
// a temporary file in the same directory (so the final rename stays on one
// volume), is flushed to disk, and is then renamed over path. The parent
// directory is created if needed. On failure the temporary file is removed and
// path is left as it was.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Chmod(perm); err != nil && runtime.GOOS != "windows" {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Antivirus or an indexer can hold the target open for a moment on Windows.
	for attempt := 0; ; attempt++ {
		if err = os.Rename(tmp, path); err == nil || attempt >= 4 {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 15 * time.Millisecond)
	}
}

// Quarantine moves an unreadable file aside to <path>.corrupt-<time> (with a
// numeric suffix if that name is taken) and returns the new name, so the user
// can look at it while the program carries on with an empty file.
func Quarantine(path string) (string, error) {
	stamp := time.Now().Format("20060102-150405")
	for n := 0; ; n++ {
		backup := fmt.Sprintf("%s.corrupt-%s", path, stamp)
		if n > 0 {
			backup = fmt.Sprintf("%s-%d", backup, n)
		}
		if _, err := os.Lstat(backup); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", err
		}
		return backup, os.Rename(path, backup)
	}
}
