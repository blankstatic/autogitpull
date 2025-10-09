package git

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/exc"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/fs"
)

const GitDirName = ".git"
const DefaultPullTimeoutSec = 5 * time.Second

func DetectRepository(path string) error {
	err := fs.CheckDirectoryExist(path)
	if err != nil {
		return err
	}

	repoPath := filepath.Join(path, GitDirName)
	return fs.CheckDirectoryExist(repoPath)
}

func GetCurrentBranch(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultPullTimeoutSec)
	defer cancel()

	output, err := exc.CommandExec(ctx, DefaultPullTimeoutSec, path, "git", "branch", "--show-current")
	if err != nil {
		return "", err
	}

	lines := strings.Split(output, "\n")
	return lines[0], nil
}

func GetRemoteDefaultBranch(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultPullTimeoutSec)
	defer cancel()

	output, err := exc.CommandExec(ctx, DefaultPullTimeoutSec, path, "git", "--no-pager", "branch", "-r")
	if err != nil {
		return "", err
	}

	lines := strings.Split(output, "\n")

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
	ctx, cancel := context.WithTimeout(context.Background(), DefaultPullTimeoutSec)
	defer cancel()

	return exc.CommandExec(ctx, DefaultPullTimeoutSec, path, "git", "pull", "origin")
}

func GitGetUncommitedChanges(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultPullTimeoutSec)
	defer cancel()

	return exc.CommandExec(ctx, DefaultPullTimeoutSec, path, "git", "status", "--porcelain")
}

func GitHasUncommitedChanges(path string) (bool, error) {
	changes, err := GitGetUncommitedChanges(path)
	return len(changes) > 0, err
}
