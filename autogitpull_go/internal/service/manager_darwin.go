//go:build darwin
// +build darwin

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
	serviceName        = "com.blankstatic.autogitpull"
	serviceDisplayName = "Auto Git Pull Daemon"
)

type darwinManager struct {
	configPath string
	interval   time.Duration
	storage    *config.StorageManager
}

func newManager(configPath string, interval time.Duration) Manager {
	storage := config.NewStorageManager(configPath)
	return &darwinManager{
		configPath: configPath,
		interval:   interval,
		storage:    storage,
	}
}

func (dm *darwinManager) getPlistPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(homeDir, "Library", "LaunchAgents", serviceName+".plist"), nil
}

func (dm *darwinManager) getExecutablePath() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}
	return exePath, nil
}

func (dm *darwinManager) Install() error {
	plistPath, err := dm.getPlistPath()
	if err != nil {
		return err
	}

	exePath, err := dm.getExecutablePath()
	if err != nil {
		return err
	}

	plistDir := filepath.Dir(plistPath)
	if err := os.MkdirAll(plistDir, 0755); err != nil {
		return fmt.Errorf("failed to create LaunchAgents directory: %w", err)
	}

	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>daemon</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>/tmp/%s.log</string>
	<key>StandardErrorPath</key>
	<string>/tmp/%s.error.log</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
	</dict>
	<key>StartInterval</key>
	<integer>%d</integer>
	<key>WorkingDirectory</key>
	<string>%s</string>
</dict>
</plist>`,
		serviceName,
		exePath,
		serviceName,
		serviceName,
		int(dm.interval.Seconds()),
		filepath.Dir(exePath),
	)

	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		return fmt.Errorf("failed to write plist file: %w", err)
	}

	return nil
}

func (dm *darwinManager) Start() error {
	plistPath, err := dm.getPlistPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		return fmt.Errorf("service not installed. Run 'install' first")
	}

	dm.Stop()

	cmd := exec.Command("launchctl", "load", plistPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to load service: %s - %w", string(output), err)
	}

	cmd = exec.Command("launchctl", "start", serviceName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start service: %s - %w", string(output), err)
	}

	return nil
}

func (dm *darwinManager) Stop() error {
	cmd := exec.Command("launchctl", "stop", serviceName)
	cmd.Run()

	cmd = exec.Command("launchctl", "unload", serviceName)
	cmd.Run()

	plistPath, err := dm.getPlistPath()
	if err == nil {
		cmd = exec.Command("launchctl", "unload", plistPath)
		cmd.Run()
	}

	return nil
}

func (dm *darwinManager) Uninstall() error {
	dm.Stop()

	plistPath, err := dm.getPlistPath()
	if err != nil {
		return err
	}

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove plist file: %w", err)
	}

	return nil
}

func (dm *darwinManager) Status() (string, error) {
	cmd := exec.Command("launchctl", "list", serviceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "not running", nil
	}

	if strings.Contains(string(output), "Could not find service") {
		return "not installed", nil
	}

	if strings.Contains(string(output), "\"PID\"") {
		return "running", nil
	}

	return "loaded but not running", nil
}
