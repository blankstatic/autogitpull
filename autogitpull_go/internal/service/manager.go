package service

import (
	"time"
)

type Manager interface {
	Install() error
	Start() error
	Stop() error
	Uninstall() error
	Status() (string, error)
}

func New(configPath string, interval time.Duration) Manager {
	return newManager(configPath, interval)
}
