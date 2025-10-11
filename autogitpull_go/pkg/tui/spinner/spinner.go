package spinner

import (
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type errMsg error

type updateTextMsg string

type model struct {
	spinner  spinner.Model
	quitting bool
	err      error
	text     string
}

func initialModel() model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return model{
		spinner: s,
		text:    "Exiting...press q to force quit",
	}
}

func (m model) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c", "й":
			m.quitting = true
			return m, tea.Quit
		default:
			return m, nil
		}

	case errMsg:
		m.err = msg
		return m, nil

	case updateTextMsg:
		m.text = string(msg)
		return m, nil

	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
}

func (m model) View() string {
	if m.err != nil {
		return m.err.Error()
	}
	str := fmt.Sprintf("\n\n   %s %s\n\n", m.spinner.View(), m.text)
	if m.quitting {
		return str + "\n"
	}
	return str
}

type Controller struct {
	program *tea.Program
}

func NewController() *Controller {
	model := initialModel()
	program := tea.NewProgram(model)

	return &Controller{
		program: program,
	}
}

func (sc *Controller) Run() {
	go func() {
		if _, err := sc.program.Run(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}()
}

func (sc *Controller) UpdateText(text string) {
	if sc.program != nil {
		sc.program.Send(updateTextMsg(text))
	}
}

func (sc *Controller) Quit() {
	if sc.program != nil {
		sc.program.Quit()
	}
}

func Run() {
	controller := NewController()
	defer controller.Quit()
	controller.Run()
}

func RunWithUpdates(updateChan <-chan string) {
	controller := NewController()
	defer controller.Quit()
	controller.Run()

	go func() {
		for text := range updateChan {
			controller.UpdateText(text)
		}
	}()
}
