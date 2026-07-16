//go:build linux
// +build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
)

const (
	systemdUnitName = "autogitpull.service"
)

type linuxManager struct {
	configPath string
	interval   time.Duration
	storage    *config.StorageManager
}

func newManager(configPath string, interval time.Duration) Manager {
	storage := config.NewStorageManager(configPath)
	return &linuxManager{
		configPath: configPath,
		interval:   interval,
		storage:    storage,
	}
}

func (lm *linuxManager) getUnitPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config directory: %w", err)
	}
	return filepath.Join(configDir, "systemd", "user", systemdUnitName), nil
}

func (lm *linuxManager) getExecutablePath() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}
	return exePath, nil
}

func (lm *linuxManager) Install() error {
	if lm.interval <= 0 {
		return fmt.Errorf("service interval must be positive")
	}

	unitPath, err := lm.getUnitPath()
	if err != nil {
		return err
	}

	exePath, err := lm.getExecutablePath()
	if err != nil {
		return err
	}

	unitDir := filepath.Dir(unitPath)
	if err := os.MkdirAll(unitDir, 0755); err != nil {
		return fmt.Errorf("failed to create systemd user directory: %w", err)
	}

	if err := os.WriteFile(unitPath, []byte(lm.unitContent(exePath)), 0644); err != nil {
		return fmt.Errorf("failed to write systemd unit: %w", err)
	}

	if err := lm.systemctl("daemon-reload"); err != nil {
		return err
	}
	lm.importDesktopEnvironment()
	if err := lm.systemctl("enable", systemdUnitName); err != nil {
		return err
	}

	return nil
}

func (lm *linuxManager) unitContent(exePath string) string {
	return fmt.Sprintf(`[Unit]
Description=Auto Git Pull Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s daemon
WorkingDirectory=%s
Restart=always
RestartSec=10
Environment=PATH=/usr/local/bin:/usr/bin:/bin:/usr/local/sbin:/usr/sbin:/sbin

[Install]
WantedBy=default.target
`,
		systemdQuote(exePath),
		systemdQuote(filepath.Dir(exePath)),
	)
}

func (lm *linuxManager) Start() error {
	unitPath, err := lm.getUnitPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		return fmt.Errorf("service not installed. Run 'install' first")
	}

	if err := lm.systemctl("daemon-reload"); err != nil {
		return err
	}
	lm.importDesktopEnvironment()
	return lm.systemctl("start", systemdUnitName)
}

func (lm *linuxManager) Stop() error {
	return lm.systemctl("stop", systemdUnitName)
}

func (lm *linuxManager) Uninstall() error {
	_ = lm.systemctl("stop", systemdUnitName)
	_ = lm.systemctl("disable", systemdUnitName)

	unitPath, err := lm.getUnitPath()
	if err != nil {
		return err
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove systemd unit: %w", err)
	}

	return lm.systemctl("daemon-reload")
}

func (lm *linuxManager) Status() (string, error) {
	unitPath, err := lm.getUnitPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		return "not installed", nil
	}

	cmd := exec.Command("systemctl", "--user", "is-active", systemdUnitName)
	output, err := cmd.CombinedOutput()
	status := strings.TrimSpace(string(output))
	if err == nil && status == "active" {
		return "running", nil
	}
	if status == "inactive" || status == "failed" || status == "activating" || status == "deactivating" {
		return status, nil
	}
	if status == "" {
		return "", fmt.Errorf("systemctl --user is-active failed: %w", err)
	}
	return status, nil
}

func (lm *linuxManager) systemctl(args ...string) error {
	cmdArgs := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s failed: %s - %w", strings.Join(args, " "), strings.TrimSpace(string(output)), err)
	}
	return nil
}

func (lm *linuxManager) importDesktopEnvironment() {
	_ = lm.systemctl(
		"import-environment",
		"DISPLAY",
		"WAYLAND_DISPLAY",
		"XAUTHORITY",
		"DBUS_SESSION_BUS_ADDRESS",
		"XDG_CURRENT_DESKTOP",
		"DESKTOP_SESSION",
	)
}

func systemdQuote(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}
