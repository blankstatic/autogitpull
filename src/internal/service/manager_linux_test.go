//go:build linux
// +build linux

package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLinuxUnitPathUsesShortServiceName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/autogitpull-config")

	manager := &linuxManager{}
	got, err := manager.getUnitPath()
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join("/tmp/autogitpull-config", "systemd", "user", "autogitpull.service")
	if got != want {
		t.Fatalf("getUnitPath() = %q, want %q", got, want)
	}
}

func TestLinuxUnitContentQuotesPaths(t *testing.T) {
	manager := &linuxManager{interval: 30 * time.Minute}
	unit := manager.unitContent(`/tmp/autogitpull "tools"/autogitpull`)

	if !strings.Contains(unit, `ExecStart="/tmp/autogitpull \"tools\"/autogitpull" daemon`) {
		t.Fatalf("quoted executable path missing from unit:\n%s", unit)
	}
	if !strings.Contains(unit, `Restart=always`) {
		t.Fatalf("expected restart policy in unit:\n%s", unit)
	}
	if !strings.Contains(unit, `WantedBy=default.target`) {
		t.Fatalf("expected user service install target in unit:\n%s", unit)
	}
}

func TestLinuxInstallRejectsNonPositiveInterval(t *testing.T) {
	manager := &linuxManager{interval: 0}
	if err := manager.Install(); err == nil {
		t.Fatal("expected non-positive interval error")
	}
}

func TestSystemdQuoteEscapesBackslashAndQuote(t *testing.T) {
	got := systemdQuote(`a\b"c`)
	want := `"a\\b\"c"`
	if got != want {
		t.Fatalf("systemdQuote() = %q, want %q", got, want)
	}
}
