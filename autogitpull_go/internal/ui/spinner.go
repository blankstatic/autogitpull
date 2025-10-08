package ui

import (
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type errMsg error

// Сообщение для обновления текста через канал
type updateTextMsg string

type spinnerModel struct {
	spinner  spinner.Model
	quitting bool
	err      error
	text     string // Текст для отображения
}

func initialSpinnerModel() spinnerModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return spinnerModel{
		spinner: s,
		text:    "Exiting...press q to force quit", // текст по умолчанию
	}
}

func (m spinnerModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m spinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			os.Exit(1)
			return m, nil
		default:
			return m, nil
		}

	case errMsg:
		m.err = msg
		return m, nil

	case updateTextMsg:
		// Обновляем текст при получении сообщения
		m.text = string(msg)
		return m, nil

	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
}

func (m spinnerModel) View() string {
	if m.err != nil {
		return m.err.Error()
	}
	str := fmt.Sprintf("\n\n   %s %s\n\n", m.spinner.View(), m.text)
	if m.quitting {
		return str + "\n"
	}
	return str
}

// Структура для управления спиннером с каналом
type SpinnerController struct {
	program *tea.Program
}

// Создает новый контроллер спиннера
func NewSpinnerController() *SpinnerController {
	model := initialSpinnerModel()
	program := tea.NewProgram(model)

	return &SpinnerController{
		program: program,
	}
}

// Запускает спиннер в отдельной горутине
func (sc *SpinnerController) Run() {
	go func() {
		if _, err := sc.program.Run(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}()
}

// Обновляет текст спиннера через канал
func (sc *SpinnerController) UpdateText(text string) {
	if sc.program != nil {
		sc.program.Send(updateTextMsg(text))
	}
}

// Останавливает спиннер
func (sc *SpinnerController) Quit() {
	if sc.program != nil {
		sc.program.Quit()
	}
}

// Функция для обратной совместимости
func RunSpinner() {
	controller := NewSpinnerController()
	controller.Run()
}

// Пример использования с каналом для обновления текста
func RunSpinnerWithUpdates(updateChan <-chan string) {
	controller := NewSpinnerController()
	controller.Run()

	// Обрабатываем обновления из канала
	go func() {
		for text := range updateChan {
			controller.UpdateText(text)
		}
	}()
}
