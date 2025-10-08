package lib

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func DetectRepository(path string) error {
	err := CheckDirectoryExist(path)
	if err != nil {
		return err
	}

	repoPath := filepath.Join(path, GitDirName)
	return CheckDirectoryExist(repoPath)
}

func GetCurrentBranch(path string) (string, error) {
	cmd := exec.Command("git", "branch", "--show-current")

	cmd.Dir = path

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(output), "\n")
	return lines[0], nil
}

func GetRemoteDefaultBranch(path string) (string, error) {
	cmd := exec.Command("git", "--no-pager", "branch", "-r")

	cmd.Dir = path

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "origin/HEAD ->") {
			parts := strings.Split(line, "->")
			if len(parts) == 2 {
				branch := strings.TrimSpace(parts[1])
				branch = strings.TrimPrefix(branch, "origin/")
				return branch, nil
			}
		}
	}

	return "", nil
}

func GitPull(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), PullTimeoutSec*time.Second)
	defer cancel()

	cmd := exec.Command("git", "pull", "origin") // Не используем CommandContext
	cmd.Dir = path

	// Устанавливаем process group для Unix-систем
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	// Запускаем команду
	err := cmd.Start()
	if err != nil {
		return "", err
	}

	// Канал для завершения команды
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Ждем либо завершения, либо таймаута
	select {
	case <-ctx.Done():
		// Таймаут - убиваем процесс
		if cmd.Process != nil {
			if runtime.GOOS != "windows" {
				// Убиваем всю группу процессов
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			} else {
				cmd.Process.Kill()
			}
		}
		<-done // Ждем освобождения ресурсов
		return out.String(), fmt.Errorf("git pull timeout after 2 seconds: %w", ctx.Err())

	case err := <-done:
		return out.String(), err
	}
}

func GitGetUncommitedChanges(path string) (string, error) {
	cmd := exec.Command("git", "status", "--porcelain")

	cmd.Dir = path

	output, err := cmd.Output()
	if err != nil {
		return string(output), err
	}

	return string(output), nil
}

func GitHasUncommitedChanges(path string) (bool, error) {
	changes, err := GitGetUncommitedChanges(path)
	return len(changes) > 0, err
}
