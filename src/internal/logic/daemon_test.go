package logic

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/blankstatic/autogitpull/src/internal/config"
)

func TestNewDaemonRejectsNonPositiveInterval(t *testing.T) {
	_, err := NewDaemon(Config{
		Interval: 0,
		Storage:  config.NewStorageManager(filepath.Join(t.TempDir(), "config.json")),
	})
	if err == nil {
		t.Fatal("expected non-positive interval error")
	}
}

func TestUpdateIntervalWhileRunningDoesNotDeadlock(t *testing.T) {
	storage := config.NewStorageManager(filepath.Join(t.TempDir(), "config.json"))
	if err := storage.Load(); err != nil {
		t.Fatal(err)
	}

	d, err := NewDaemon(Config{
		Interval: time.Hour,
		Storage:  storage,
	})
	if err != nil {
		t.Fatal(err)
	}
	d.Start()
	defer d.Stop()

	done := make(chan struct{})
	go func() {
		d.UpdateInterval(2 * time.Hour)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("UpdateInterval deadlocked")
	}
	if !d.IsRunning() {
		t.Fatal("expected daemon to keep running")
	}
}
