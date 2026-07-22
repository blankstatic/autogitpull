//go:build !windows

package pulllock

import (
	"os"
	"strconv"
	"syscall"
)

func lockIdentity() string {
	return strconv.Itoa(os.Getuid())
}

func tryFileLock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func closeFileLock(file *os.File) error {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return file.Close()
}
