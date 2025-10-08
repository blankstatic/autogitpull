//go:build darwin
// +build darwin

package service

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/lib"
)

const (
	serviceName        = "com.blankstatic.autogitpull"
	serviceDisplayName = "Auto Git Pull Daemon"
)

type darwinManager struct {
	configPath string
	interval   time.Duration
	storage    *lib.StorageManager
}

func newManager(configPath string, interval time.Duration) Manager {
	storage := lib.NewStorageManager(configPath)
	return &darwinManager{
		configPath: configPath,
		interval:   interval,
		storage:    storage,
	}
}

// getPlistPath возвращает путь к файлу .plist
func (dm *darwinManager) getPlistPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(homeDir, "Library", "LaunchAgents", serviceName+".plist"), nil
}

// getExecutablePath возвращает путь к исполняемому файлу
func (dm *darwinManager) getExecutablePath() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}
	return exePath, nil
}

// Install устанавливает службу launchd
func (dm *darwinManager) Install() error {
	plistPath, err := dm.getPlistPath()
	if err != nil {
		return err
	}

	exePath, err := dm.getExecutablePath()
	if err != nil {
		return err
	}

	// Создаем директорию если не существует
	plistDir := filepath.Dir(plistPath)
	if err := os.MkdirAll(plistDir, 0755); err != nil {
		return fmt.Errorf("failed to create LaunchAgents directory: %w", err)
	}

	// Создаем содержимое .plist файла
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
		// dm.configPath,
		serviceName,
		serviceName,
		int(dm.interval.Seconds()),
		filepath.Dir(exePath),
	)

	// Записываем .plist файл
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		return fmt.Errorf("failed to write plist file: %w", err)
	}

	log.Printf("Service plist installed at: %s", plistPath)
	return nil
}

// Load загружает и запускает службу
func (dm *darwinManager) Start() error {
	plistPath, err := dm.getPlistPath()
	if err != nil {
		return err
	}

	// Проверяем существует ли .plist файл
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		return fmt.Errorf("service not installed. Run 'install' first")
	}

	// Выгружаем службу если уже загружена
	dm.Stop()

	// Загружаем службу
	cmd := exec.Command("launchctl", "load", plistPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to load service: %s - %w", string(output), err)
	}

	// Запускаем службу
	cmd = exec.Command("launchctl", "start", serviceName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start service: %s - %w", string(output), err)
	}

	log.Printf("Service '%s' started successfully", serviceName)
	return nil
}

// Stop останавливает службу
func (dm *darwinManager) Stop() error {
	// Останавливаем службу
	cmd := exec.Command("launchctl", "stop", serviceName)
	cmd.Run() // Игнорируем ошибку, если служба не запущена

	// Выгружаем службу
	cmd = exec.Command("launchctl", "unload", serviceName)
	cmd.Run() // Игнорируем ошибку, если служба не загружена

	// Также пытаемся выгрузить по полному пути
	plistPath, err := dm.getPlistPath()
	if err == nil {
		cmd = exec.Command("launchctl", "unload", plistPath)
		cmd.Run()
	}

	log.Printf("Service '%s' stopped successfully", serviceName)
	return nil
}

// Uninstall удаляет службу
func (dm *darwinManager) Uninstall() error {
	// Сначала останавливаем
	dm.Stop()

	// Удаляем .plist файл
	plistPath, err := dm.getPlistPath()
	if err != nil {
		return err
	}

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove plist file: %w", err)
	}

	log.Printf("Service '%s' uninstalled successfully", serviceName)
	return nil
}

// Status проверяет статус службы
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
