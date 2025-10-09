package exc

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

const DefaultGracefulShutdownTimeout = 2 * time.Second

func CommandExec(
	ctx context.Context,
	timeout time.Duration,
	path string,
	commandName string,
	commandArgs ...string,
) (string, error) {
	var cmdCtx context.Context
	var cancel context.CancelFunc

	if timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, commandName, commandArgs...)
	cmd.Dir = path

	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Start()
	if err != nil {
		return "", fmt.Errorf("failed to start command: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		return killProcess(cmd, done), fmt.Errorf("command cancelled by context: %w", ctx.Err())

	case <-cmdCtx.Done():
		return killProcess(cmd, done), fmt.Errorf("command timeout after %v: %w", timeout, cmdCtx.Err())

	case err := <-done:
		if err != nil {
			return out.String(), fmt.Errorf("command execution failed: %w", err)
		}
		return out.String(), nil
	}
}

func killProcess(cmd *exec.Cmd, done chan error) string {
	if cmd.Process == nil {
		return ""
	}

	if runtime.GOOS != "windows" {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	} else {
		cmd.Process.Signal(syscall.SIGTERM)
	}

	select {
	case <-done:
		// ok
	case <-time.After(DefaultGracefulShutdownTimeout):
		if runtime.GOOS != "windows" {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		} else {
			cmd.Process.Kill()
		}
		<-done
	}

	var buf bytes.Buffer
	if cmd.Stdout != nil {
		if out, ok := cmd.Stdout.(*bytes.Buffer); ok {
			buf.Write(out.Bytes())
		}
	}
	if cmd.Stderr != nil {
		if err, ok := cmd.Stderr.(*bytes.Buffer); ok {
			buf.Write(err.Bytes())
		}
	}

	return buf.String()
}
