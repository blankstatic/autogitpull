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
	output, _, err := commandExec(ctx, timeout, path, 0, commandName, commandArgs...)
	return output, err
}

// CommandExecLimited runs a command while retaining at most maxOutputBytes of
// combined stdout/stderr. The command is allowed to finish normally after the
// limit is reached, but additional output is discarded to keep memory bounded.
func CommandExecLimited(
	ctx context.Context,
	timeout time.Duration,
	path string,
	maxOutputBytes int,
	commandName string,
	commandArgs ...string,
) (string, bool, error) {
	return commandExec(ctx, timeout, path, maxOutputBytes, commandName, commandArgs...)
}

func commandExec(
	ctx context.Context,
	timeout time.Duration,
	path string,
	maxOutputBytes int,
	commandName string,
	commandArgs ...string,
) (string, bool, error) {
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

	out := &limitedBuffer{limit: maxOutputBytes}
	cmd.Stdout = out
	cmd.Stderr = out

	err := cmd.Start()
	if err != nil {
		return "", false, fmt.Errorf("failed to start command: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		return killProcess(cmd, done), out.truncated, fmt.Errorf("command cancelled by context: %w", ctx.Err())

	case <-cmdCtx.Done():
		return killProcess(cmd, done), out.truncated, fmt.Errorf("command timeout after %v: %w", timeout, cmdCtx.Err())

	case err := <-done:
		if err != nil {
			return out.String(), out.truncated, fmt.Errorf("command execution failed: %w", err)
		}
		return out.String(), out.truncated, nil
	}
}

type limitedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	originalLen := len(p)
	if b.limit <= 0 {
		_, err := b.Buffer.Write(p)
		return originalLen, err
	}
	remaining := b.limit - b.Buffer.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || originalLen > 0
		return originalLen, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, err := b.Buffer.Write(p)
	return originalLen, err
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
		} else if out, ok := cmd.Stdout.(*limitedBuffer); ok {
			buf.Write(out.Bytes())
		}
	}
	if cmd.Stderr != nil && cmd.Stderr != cmd.Stdout {
		if err, ok := cmd.Stderr.(*bytes.Buffer); ok {
			buf.Write(err.Bytes())
		}
	}

	return buf.String()
}
