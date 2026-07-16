package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var DefaultSkipDirs = []string{
	".git",
	"node_modules",
	"vendor",
	"__pycache__",
	".idea",
	".vscode",
	"build",
	"dist",
	"target",
	".cache",
	"tmp",
	"temp",
}

func GetUserHomeDir() (string, error) {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		err = fmt.Errorf("unable to compute path to home directory: %w", err)
		return "", fmt.Errorf("unable to compute path to home directory: %w", err)
	}
	return homeDirectory, nil
}

// CompactHomePath hides the local username while keeping a path recognizable.
func CompactHomePath(path string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		return path
	}
	homePrefix := strings.TrimRight(homeDir, string(filepath.Separator)) + string(filepath.Separator)
	if strings.HasPrefix(path, homePrefix) {
		return "~" + string(filepath.Separator) + strings.TrimPrefix(path, homePrefix)
	}
	return path
}

func GetAppDataDir(appBaseDirName string) (string, error) {
	homeDir, err := GetUserHomeDir()
	if err != nil {
		return "", err
	}
	dataDir := filepath.Join(homeDir, appBaseDirName)
	return dataDir, err
}

func GetCurrentDirectory() (string, error) {
	currentDirectory, err := os.Getwd()
	if err != nil {
		err = fmt.Errorf("unable to compute path to current directory: %w", err)
		return "", err
	}
	return currentDirectory, nil
}

func PathToAbsPath(path string) (string, error) {
	var err error

	if !filepath.IsAbs(path) {
		path, err = filepath.Abs(path)
		if err != nil {
			return path, fmt.Errorf("path error: %w", err)
		}
	}
	return path, nil
}

func CheckDirectoryExist(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("path error: %w", err)
	}
	if os.IsNotExist(err) {
		return fmt.Errorf("path is not exist: %s", path)
	}
	if !fi.IsDir() {
		return fmt.Errorf("path is not a directory: %s", path)
	}
	return nil
}

func FindDirectories(
	rootPath string,
	detectFunc func(string) error,
	progressCallback func(string),
	skipDirs []string,
) ([]string, error) {
	var result []string

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				return filepath.SkipDir
			}
			return err
		}

		if info.IsDir() {
			if progressCallback != nil {
				progressCallback(path)
			}

			base := filepath.Base(path)

			if ShouldSkipDirectory(base, skipDirs) {
				return filepath.SkipDir
			}

			detectErr := detectFunc(path)
			if detectErr != nil {
				return nil
			}
			result = append(result, path)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("directory walk err: %w", err)
	}

	return result, nil
}

func ShouldSkipDirectory(name string, skipDirs []string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}

	if slices.Contains(skipDirs, name) {
		return true
	}

	return false
}
