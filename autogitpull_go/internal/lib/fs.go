package lib

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func GetUserHomeDir() (string, error) {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		err = fmt.Errorf("unable to compute path to home directory: %w", err)
		return "", fmt.Errorf("unable to compute path to home directory: %w", err)
	}
	return homeDirectory, nil
}

func GetAppDataDir() (string, error) {
	homeDir, err := GetUserHomeDir()
	if err != nil {
		return "", err
	}
	dataDir := filepath.Join(homeDir, AppDataDir)
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

func FindDirectories(rootPath string, detectFunc func(string) error, progressCallback func(string)) ([]string, error) {
	var result []string

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				return filepath.SkipDir
			}
			return err
		}

		if info.IsDir() {
			// Вызываем callback с текущим путем
			if progressCallback != nil {
				progressCallback(path)
			}

			base := filepath.Base(path)

			if ShouldSkipDirectory(base) {
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

func ShouldSkipDirectory(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}

	if slices.Contains(SkipDirs, name) {
		return true
	}

	return false
}
