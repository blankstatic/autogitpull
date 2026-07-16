//go:build darwin

package web

import "os/exec"

func openRepoTarget(repoPath, target string) error {
	var cmd *exec.Cmd
	switch target {
	case "code":
		cmd = exec.Command("open", "-a", "Visual Studio Code", repoPath)
	case "terminal":
		cmd = exec.Command("open", "-a", "Terminal", repoPath)
	default:
		cmd = exec.Command("open", repoPath)
	}
	return cmd.Start()
}
