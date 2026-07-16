//go:build linux

package notifications

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"
)

func customNotify(title, body, openURL string) error {
	if openURL == "" {
		return errors.New("missing notification URL")
	}
	if _, err := exec.LookPath("notify-send"); err != nil {
		return err
	}
	if _, err := exec.LookPath("xdg-open"); err != nil {
		return err
	}
	if !notifySendSupportsActions() {
		return errors.New("notify-send actions are not supported")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)

	cmd := exec.CommandContext(
		ctx,
		"notify-send",
		"--app-name", "autogitpull",
		"--icon", "dialog-information",
		"--action", "open=Open",
		"--wait",
		title,
		body,
	)
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}

	go func() {
		defer cancel()
		actionBytes, _ := io.ReadAll(output)
		_ = cmd.Wait()
		if strings.TrimSpace(string(actionBytes)) != "open" {
			return
		}

		openCtx, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer openCancel()
		_ = exec.CommandContext(openCtx, "xdg-open", openURL).Start()
	}()

	return nil
}

func notifySendSupportsActions() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, "notify-send", "--help").CombinedOutput()
	if err != nil {
		return false
	}
	help := string(output)
	return strings.Contains(help, "--action") && strings.Contains(help, "--wait")
}
