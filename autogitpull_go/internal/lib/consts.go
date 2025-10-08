package lib

import (
	_ "embed"
)

const (
	AppVersion     = "v0.1"
	AppName        = "autogitpull"
	AppDataDir     = ".autogitpull"
	GitDirName     = ".git"
	ConfigFilename = "config.json"
	PullTimeoutSec = 5
)

//go:embed assets/info.png
var InfoIcon []byte

var SkipDirs = []string{
	".git", // уже обрабатываем отдельно
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
