//go:build darwin

package notifications

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	defaultNotifierAppName = "FeatureHubLauncher.app"
	defaultDashboardURL    = "http://localhost:9009"
)

func customNotify(title, body, openURL string) error {
	notifierBin, err := findNotifierBinary()
	if err != nil {
		return err
	}

	dashboardURL := os.Getenv("AUTOGITPULL_DASHBOARD_URL")
	if openURL != "" {
		dashboardURL = openURL
	}
	if dashboardURL == "" {
		dashboardURL = defaultDashboardURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		notifierBin,
		"-title", title,
		"-message", body,
		"-open", dashboardURL,
	)

	if output, err := cmd.CombinedOutput(); err != nil {
		if len(output) > 0 {
			return errors.New(string(output))
		}
		return err
	}

	return nil
}

func findNotifierBinary() (string, error) {
	if appPath := os.Getenv("AUTOGITPULL_NOTIFIER_APP"); appPath != "" {
		return notifierBinary(appPath)
	}

	homeDir, err := os.UserHomeDir()
	if err == nil {
		if bin, err := notifierBinary(filepath.Join(homeDir, "Applications", defaultNotifierAppName)); err == nil {
			return bin, nil
		}
	}

	return notifierBinary(filepath.Join("/Applications", defaultNotifierAppName))
}

func notifierBinary(appPath string) (string, error) {
	bin := filepath.Join(appPath, "Contents", "MacOS", "terminal-notifier")
	if stat, err := os.Stat(bin); err == nil && !stat.IsDir() && stat.Mode()&0111 != 0 {
		return bin, nil
	}

	return "", os.ErrNotExist
}
