//go:build !darwin && !linux
// +build !darwin,!linux

package service

import (
	"fmt"
	"time"
)

type otherManager struct {
	configPath string
	interval   time.Duration
}

func newManager(configPath string, interval time.Duration) Manager {
	return &otherManager{
		configPath: configPath,
		interval:   interval,
	}
}

func (om *otherManager) Install() error {
	return fmt.Errorf("service management not supported on this platform")
}

func (om *otherManager) Start() error {
	return fmt.Errorf("service management not supported on this platform")
}

func (om *otherManager) Stop() error {
	return fmt.Errorf("service management not supported on this platform")
}

func (om *otherManager) Uninstall() error {
	return fmt.Errorf("service management not supported on this platform")
}

func (om *otherManager) Status() (string, error) {
	return "not supported", fmt.Errorf("service management not supported on this platform")
}
