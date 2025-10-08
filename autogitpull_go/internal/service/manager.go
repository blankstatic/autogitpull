package service

import (
	"time"
)

// Manager определяет общий интерфейс для управления службами
type Manager interface {
	Install() error
	Start() error
	Stop() error
	Uninstall() error
	Status() (string, error)
}

// New создает менеджер служб для текущей платформы
func New(configPath string, interval time.Duration) Manager {
	return newManager(configPath, interval)
}
