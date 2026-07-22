//go:build windows

package pulllock

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockIdentity() string {
	if cacheDir, err := os.UserCacheDir(); err == nil {
		return cacheDir
	}
	return os.Getenv("USERNAME")
}

func tryFileLock(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}

func closeFileLock(file *os.File) error {
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
	return file.Close()
}
