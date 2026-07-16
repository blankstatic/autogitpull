package pulllock

import "testing"

func TestTryLock(t *testing.T) {
	path := t.TempDir()
	if !TryLock(path) {
		t.Fatal("expected first lock to succeed")
	}
	if TryLock(path) {
		t.Fatal("expected second lock to fail")
	}
	Unlock(path)
	if !TryLock(path) {
		t.Fatal("expected lock to succeed after unlock")
	}
	Unlock(path)
}
