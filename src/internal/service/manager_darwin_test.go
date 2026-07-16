//go:build darwin
// +build darwin

package service

import (
	"strings"
	"testing"
	"time"
)

func TestDarwinPlistContentEscapesPaths(t *testing.T) {
	manager := &darwinManager{interval: 30 * time.Minute}
	plist := manager.plistContent("/tmp/autogitpull & tools/autogitpull")

	if strings.Contains(plist, "/tmp/autogitpull & tools/autogitpull") {
		t.Fatal("expected executable path to be XML-escaped")
	}
	if !strings.Contains(plist, "/tmp/autogitpull &amp; tools/autogitpull") {
		t.Fatalf("escaped executable path missing from plist:\n%s", plist)
	}
	if !strings.Contains(plist, "<integer>1800</integer>") {
		t.Fatalf("expected interval seconds in plist:\n%s", plist)
	}
}

func TestDarwinInstallRejectsNonPositiveInterval(t *testing.T) {
	manager := &darwinManager{interval: 0}
	if err := manager.Install(); err == nil {
		t.Fatal("expected non-positive interval error")
	}
}
