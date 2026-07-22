package pulllock

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type heldLock struct {
	file *os.File
}

var locks sync.Map

func TryLock(path string) bool {
	if path == "" {
		return false
	}
	key := canonicalPath(path)
	placeholder := &heldLock{}
	_, loaded := locks.LoadOrStore(key, placeholder)
	if loaded {
		return false
	}

	file, err := openFileLock(key)
	if err != nil {
		locks.Delete(key)
		return false
	}
	placeholder.file = file
	return true
}

func Unlock(path string) {
	if path == "" {
		return
	}
	key := canonicalPath(path)
	value, ok := locks.LoadAndDelete(key)
	if !ok {
		return
	}
	lock := value.(*heldLock)
	if lock.file != nil {
		_ = closeFileLock(lock.file)
	}
}

func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

func openFileLock(path string) (*os.File, error) {
	identity := lockIdentity()
	identityHash := sha256.Sum256([]byte(identity))
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("autogitpull-pull-locks-%x", identityHash[:8]))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("%x.lock", sha256.Sum256([]byte(path)))
	file, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := tryFileLock(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
