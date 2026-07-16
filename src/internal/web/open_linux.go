//go:build linux

package web

import (
	"fmt"
	"os/exec"
)

func openRepoTarget(repoPath, target string) error {
	switch target {
	case "code":
		return exec.Command("code", repoPath).Start()
	case "terminal":
		return openLinuxTerminal(repoPath)
	default:
		return exec.Command("xdg-open", repoPath).Start()
	}
}

func openLinuxTerminal(repoPath string) error {
	candidates := []struct {
		name string
		args []string
	}{
		{name: "x-terminal-emulator", args: []string{"--working-directory", repoPath}},
		{name: "gnome-terminal", args: []string{"--working-directory", repoPath}},
		{name: "konsole", args: []string{"--workdir", repoPath}},
		{name: "xfce4-terminal", args: []string{"--working-directory", repoPath}},
		{name: "xterm", args: []string{"-e", "sh", "-lc", "cd \"$1\" && exec ${SHELL:-sh}", "autogitpull-terminal", repoPath}},
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate.name)
		if err != nil {
			continue
		}
		return exec.Command(path, candidate.args...).Start()
	}
	return fmt.Errorf("no supported terminal emulator found")
}
