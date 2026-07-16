package pulllock

import "sync"

var locks sync.Map

func TryLock(path string) bool {
	if path == "" {
		return false
	}
	_, loaded := locks.LoadOrStore(path, struct{}{})
	return !loaded
}

func Unlock(path string) {
	if path == "" {
		return
	}
	locks.Delete(path)
}
