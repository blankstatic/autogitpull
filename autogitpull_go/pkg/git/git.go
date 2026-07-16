package git

import (
	"context"
	"net/url"
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

func GetRemoteWebURL(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultPullTimeoutSec)
	defer cancel()

	output, err := exc.CommandExec(ctx, DefaultPullTimeoutSec, path, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return RemoteWebURL(strings.TrimSpace(output)), nil
}

func RemoteWebURL(remote string) string {
	remote = strings.TrimSpace(remote)
	if strings.HasPrefix(remote, "git@") {
		parts := strings.SplitN(strings.TrimPrefix(remote, "git@"), ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return ""
		}
		return "https://" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
	}

	parsed, err := url.Parse(remote)
	if err != nil || parsed.Host == "" {
		return ""
	}
	switch parsed.Scheme {
	case "http", "https":
		parsed.Path = strings.TrimSuffix(parsed.Path, ".git")
		return parsed.String()
	case "ssh":
		parsed.Scheme = "https"
		parsed.User = nil
		parsed.Path = strings.TrimSuffix(parsed.Path, ".git")
		return parsed.String()
	default:
		return ""
	}
}

func GitPull(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultPullTimeoutSec)
	defer cancel()

	return exc.CommandExec(ctx, DefaultPullTimeoutSec, path, "git", "pull", "origin")
}

func GitHead(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultPullTimeoutSec)
	defer cancel()

	output, err := exc.CommandExec(ctx, DefaultPullTimeoutSec, path, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func GitChangedLog(path, fromRev, toRev string) (string, error) {
	if fromRev == "" || toRev == "" || fromRev == toRev {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultPullTimeoutSec)
	defer cancel()

	return exc.CommandExec(ctx, DefaultPullTimeoutSec, path, "git", "--no-pager", "log", "--stat", "--oneline", fromRev+".."+toRev)
}

func GitDiffStat(path, fromRev, toRev string) (string, error) {
	if fromRev == "" || toRev == "" || fromRev == toRev {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultPullTimeoutSec)
	defer cancel()

	return exc.CommandExec(ctx, DefaultPullTimeoutSec, path, "git", "--no-pager", "diff", "--stat", fromRev+".."+toRev)
}

func GitChangedFiles(path, fromRev, toRev string) ([]string, error) {
	if fromRev == "" || toRev == "" || fromRev == toRev {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultPullTimeoutSec)
	defer cancel()

	output, err := exc.CommandExec(ctx, DefaultPullTimeoutSec, path, "git", "--no-pager", "diff", "--name-only", fromRev+".."+toRev)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

func GitDiffPatchForFile(path, fromRev, toRev, filePath string) (string, error) {
	if fromRev == "" || toRev == "" || fromRev == toRev || filePath == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultPullTimeoutSec)
	defer cancel()

	return exc.CommandExec(ctx, DefaultPullTimeoutSec, path, "git", "--no-pager", "diff", "--find-renames", "--unified=80", fromRev+".."+toRev, "--", filePath)
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
