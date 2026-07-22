package pulllock

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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

func TestTryLockAcrossProcesses(t *testing.T) {
	if path := os.Getenv("AUTOGITPULL_TEST_LOCK_PATH"); path != "" {
		if TryLock(path) {
			Unlock(path)
			os.Exit(2)
		}
		os.Exit(0)
	}

	path := t.TempDir()
	if !TryLock(path) {
		t.Fatal("expected parent lock to succeed")
	}
	defer Unlock(path)
	cmd := exec.Command(os.Args[0], "-test.run=^TestTryLockAcrossProcesses$")
	cmd.Env = append(os.Environ(), "AUTOGITPULL_TEST_LOCK_PATH="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("another process acquired the lock: %v: %s", err, output)
	}
}

func TestTryLockCanonicalizesSymlink(t *testing.T) {
	realPath := t.TempDir()
	symlink := filepath.Join(t.TempDir(), "repo")
	if err := os.Symlink(realPath, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !TryLock(realPath) {
		t.Fatal("expected first lock to succeed")
	}
	defer Unlock(realPath)
	if TryLock(symlink) {
		Unlock(symlink)
		t.Fatal("symlink must not bypass repository lock")
	}
}
