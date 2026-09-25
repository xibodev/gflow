package util

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// WriteFileAtomic writes data to path so readers never observe a torn file:
// the bytes go to a temp file in the same directory, are fsynced, and are
// then renamed over the destination. Parent directories are created (0700
// when perm is private, 0755 otherwise).
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	dirPerm := os.FileMode(0o755)
	if perm&0o077 == 0 {
		dirPerm = 0o700
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpName, perm); err != nil {
			cleanup()
			return err
		}
	}
	if err := RenameWithRetry(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// RenameWithRetry renames src over dst. On Windows a rename fails while
// another process holds dst open without FILE_SHARE_DELETE (a concurrent
// reader, an antivirus scan); those failures are transient, so the rename is
// retried with a short backoff before giving up.
func RenameWithRetry(src, dst string) error {
	var err error
	delay := 20 * time.Millisecond
	for attempt := 0; attempt < 10; attempt++ {
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
		if runtime.GOOS != "windows" || errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(delay)
		if delay < 400*time.Millisecond {
			delay *= 2
		}
	}
	return fmt.Errorf("rename %s -> %s: %w", src, dst, err)
}

// windowsReserved lists device names Windows refuses as file base names.
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// SafeFileComponent reduces s to a portable file-name component: only ASCII
// letters, digits, '-' and '_' survive (everything else becomes '_'), runs of
// '_' collapse, and the result is at most max bytes. Path separators, drive
// letters, NTFS stream markers (':'), dots, and control bytes can therefore
// never reach a file name. An empty result means "no usable characters".
func SafeFileComponent(s string, max int) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-'
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_-")
	if max > 0 && len(out) > max {
		out = strings.TrimRight(out[:max], "_-")
	}
	if windowsReserved[strings.ToUpper(out)] {
		out += "_"
	}
	return out
}
