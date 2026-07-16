//go:build !darwin && !linux

package web

import "fmt"

func openRepoTarget(repoPath, target string) error {
	return fmt.Errorf("opening repositories is not supported on this platform")
}
