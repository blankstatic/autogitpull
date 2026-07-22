package git

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blankstatic/autogitpull/src/pkg/exc"
	"github.com/blankstatic/autogitpull/src/pkg/fs"
)

const GitDirName = ".git"
const (
	DefaultCommandTimeout = 5 * time.Second
	DefaultPullTimeout    = 5 * time.Minute
	staleIndexLockMinAge  = time.Hour
)

func DetectRepository(path string) error {
	err := fs.CheckDirectoryExist(path)
	if err != nil {
		return err
	}

	repoPath := filepath.Join(path, GitDirName)
	return fs.CheckDirectoryExist(repoPath)
}

func GetCurrentBranch(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultCommandTimeout)
	defer cancel()

	output, err := exc.CommandExec(ctx, DefaultCommandTimeout, path, "git", "branch", "--show-current")
	if err != nil {
		return "", err
	}

	lines := strings.Split(output, "\n")
	return lines[0], nil
}

func GetRemoteDefaultBranch(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultCommandTimeout)
	defer cancel()

	output, err := exc.CommandExec(ctx, DefaultCommandTimeout, path, "git", "--no-pager", "branch", "-r")
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
	ctx, cancel := context.WithTimeout(context.Background(), DefaultCommandTimeout)
	defer cancel()

	output, err := exc.CommandExec(ctx, DefaultCommandTimeout, path, "git", "remote", "get-url", "origin")
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
	output, err := gitPullOnce(path)
	if err == nil || !isIndexLockError(output, err) {
		return output, err
	}
	removed, cleanupErr := removeStaleIndexLock(path, time.Now())
	if cleanupErr != nil || !removed {
		return output, err
	}

	time.Sleep(100 * time.Millisecond)
	retryOutput, retryErr := gitPullOnce(path)
	if retryErr == nil {
		return "Recovered a stale Git index lock and retried the pull.\n" + retryOutput, nil
	}
	if output != "" && retryOutput != "" {
		output += "\n"
	}
	return output + retryOutput, retryErr
}

func gitPullOnce(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultPullTimeout)
	defer cancel()

	return exc.CommandExec(ctx, DefaultPullTimeout, path, "git", "pull", "origin")
}

func isIndexLockError(output string, err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(output + "\n" + err.Error())
	return strings.Contains(message, "index.lock") &&
		(strings.Contains(message, "unable to create") || strings.Contains(message, "file exists"))
}

func removeStaleIndexLock(repoPath string, now time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultCommandTimeout)
	defer cancel()
	lockPath, err := exc.CommandExec(ctx, DefaultCommandTimeout, repoPath, "git", "rev-parse", "--git-path", "index.lock")
	if err != nil {
		return false, err
	}
	lockPath = strings.TrimSpace(lockPath)
	if !filepath.IsAbs(lockPath) {
		lockPath = filepath.Join(repoPath, lockPath)
	}
	info, err := os.Lstat(lockPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || now.Sub(info.ModTime()) < staleIndexLockMinAge {
		return false, nil
	}
	if err := os.Remove(lockPath); err != nil {
		return false, err
	}
	return true, nil
}

func GitHead(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultCommandTimeout)
	defer cancel()

	output, err := exc.CommandExec(ctx, DefaultCommandTimeout, path, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func GitChangedLog(path, fromRev, toRev string) (string, error) {
	return GitChangedLogContext(context.Background(), path, fromRev, toRev)
}

func GitChangedLogContext(ctx context.Context, path, fromRev, toRev string) (string, error) {
	if fromRev == "" || toRev == "" || fromRev == toRev {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultCommandTimeout)
	defer cancel()

	return exc.CommandExec(ctx, DefaultCommandTimeout, path, "git", "--no-pager", "log", "--oneline", "--no-decorate", fromRev+".."+toRev)
}

func GitDiffStat(path, fromRev, toRev string) (string, error) {
	return GitDiffStatContext(context.Background(), path, fromRev, toRev)
}

func GitDiffStatContext(ctx context.Context, path, fromRev, toRev string) (string, error) {
	if fromRev == "" || toRev == "" || fromRev == toRev {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultCommandTimeout)
	defer cancel()

	return exc.CommandExec(ctx, DefaultCommandTimeout, path, "git", "--no-pager", "diff", "--stat", fromRev+".."+toRev)
}

func GitChangedFiles(path, fromRev, toRev string) ([]string, error) {
	return GitChangedFilesContext(context.Background(), path, fromRev, toRev)
}

func GitChangedFilesContext(ctx context.Context, path, fromRev, toRev string) ([]string, error) {
	if fromRev == "" || toRev == "" || fromRev == toRev {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultCommandTimeout)
	defer cancel()

	output, err := exc.CommandExec(ctx, DefaultCommandTimeout, path, "git", "--no-pager", "diff", "--name-only", fromRev+".."+toRev)
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
	output, _, err := GitDiffPatchForFileLimited(path, fromRev, toRev, filePath, 0)
	return output, err
}

func GitDiffPatchForFileLimited(path, fromRev, toRev, filePath string, maxBytes int) (string, bool, error) {
	return GitDiffPatchForFileLimitedContext(path, fromRev, toRev, filePath, maxBytes, 80)
}

func GitDiffPatchForFileLimitedContext(path, fromRev, toRev, filePath string, maxBytes, contextLines int) (string, bool, error) {
	return GitDiffPatchForFileLimitedContextWithContext(context.Background(), path, fromRev, toRev, filePath, maxBytes, contextLines)
}

func GitDiffPatchForFileLimitedContextWithContext(parent context.Context, path, fromRev, toRev, filePath string, maxBytes, contextLines int) (string, bool, error) {
	if fromRev == "" || toRev == "" || fromRev == toRev || filePath == "" {
		return "", false, nil
	}
	if contextLines < 0 {
		contextLines = 0
	}
	if contextLines > 200 {
		contextLines = 200
	}
	ctx, cancel := context.WithTimeout(parent, DefaultCommandTimeout)
	defer cancel()

	return exc.CommandExecLimited(ctx, DefaultCommandTimeout, path, maxBytes, "git", "--no-pager", "diff", "--find-renames", fmt.Sprintf("--unified=%d", contextLines), fromRev+".."+toRev, "--", filePath)
}

func GitGetUncommitedChanges(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultCommandTimeout)
	defer cancel()

	return exc.CommandExec(ctx, DefaultCommandTimeout, path, "git", "status", "--porcelain")
}

func GitHasUncommitedChanges(path string) (bool, error) {
	changes, err := GitGetUncommitedChanges(path)
	return len(changes) > 0, err
}
