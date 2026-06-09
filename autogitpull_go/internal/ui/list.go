package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/git"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/notifications"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type branchUpdateMsg struct {
	index         int
	currentBranch string
}

type statusUpdateMsg struct {
	path   string
	status string
}

var isSilentlyLocal bool
var (
	baseStyle = lipgloss.NewStyle().
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			MarginTop(1)

	selectedRepoStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("229")).
				Background(lipgloss.Color("57")).
				Bold(false)
)

type model struct {
	wg           *sync.WaitGroup
	table        table.Model
	helpText     string
	repos        []config.RepoInfo
	initialRepos []config.RepoInfo
	windowWidth  int
	windowHeight int
	branches     []string
	statuses     []string
	program      *tea.Program
}

const (
	tableCellHorizontalFrameSize = 2
	tableHeaderHeight            = 2
	minTableHeight               = 4
	helpViewHeight               = 2
)

func (m model) Init() tea.Cmd {
	return tea.Batch(
		tea.WindowSize(),
		m.loadBranchesAsync(),
	)
}

func (m model) loadBranchesAsync() tea.Cmd {
	return func() tea.Msg {
		var wg sync.WaitGroup
		branchChan := make(chan branchUpdateMsg, len(m.repos))

		for i, repo := range m.repos {
			wg.Add(1)
			go func(index int, path string) {
				defer wg.Done()
				currentBranch, err := git.GetCurrentBranch(path)
				if err != nil {
					currentBranch = "FAIL"
				}
				branchChan <- branchUpdateMsg{
					index:         index,
					currentBranch: currentBranch,
				}
			}(i, repo.Path)
		}

		go func() {
			wg.Wait()
			close(branchChan)
		}()

		var updates []branchUpdateMsg
		for update := range branchChan {
			updates = append(updates, update)
		}

		return updates
	}
}

func updateTableWidth(t table.Model, totalWidth int) table.Model {
	columnPercentages := []int{15, 15, 15, 45, 10}

	tableFrameWidth := len(columnPercentages) * tableCellHorizontalFrameSize
	availableWidth := totalWidth - baseStyle.GetHorizontalFrameSize() - tableFrameWidth
	if availableWidth < len(columnPercentages) {
		availableWidth = len(columnPercentages)
	}

	columns := t.Columns()
	updatedColumns := make([]table.Column, len(columns))
	usedWidth := 0
	for i, col := range columns {
		colWidth := (availableWidth * columnPercentages[i]) / 100
		if i == len(columns)-1 {
			colWidth = availableWidth - usedWidth
		}
		if colWidth < 1 {
			colWidth = 1
		}
		usedWidth += colWidth
		updatedColumns[i] = table.Column{
			Title: col.Title,
			Width: colWidth,
		}
	}

	t.SetColumns(updatedColumns)
	t.SetWidth(availableWidth)
	return t
}

func (m model) getSelectedRepo() *config.RepoInfo {
	if len(m.table.SelectedRow()) == 0 || m.table.Cursor() >= len(m.repos) {
		return nil
	}
	return &m.repos[m.table.Cursor()]
}

func (m model) removeRepo(path string) []config.RepoInfo {
	var newRepos []config.RepoInfo
	for _, repo := range m.repos {
		if repo.Path != path {
			newRepos = append(newRepos, repo)
		}
	}
	return newRepos
}

func (m *model) setStatusByPath(path string, status string) {
	for i, repo := range m.repos {
		if repo.Path == path && i < len(m.statuses) {
			m.statuses[i] = status
			break
		}
	}
}

func (m *model) sendStatusUpdate(path string, status string) {
	if m.program != nil {
		m.program.Send(statusUpdateMsg{
			path:   path,
			status: status,
		})
	}
}

func (m *model) updateTableRows() {
	rows := []table.Row{}
	for i, repo := range m.repos {
		var currentBranch string
		var status string

		if i < len(m.branches) && m.branches[i] != "" {
			currentBranch = m.branches[i]
		} else {
			currentBranch = "..."
		}

		if i < len(m.statuses) && m.statuses[i] != "" {
			status = m.statuses[i]
		} else {
			status = "Ready"
		}

		rows = append(rows, table.Row{
			repo.Name,
			currentBranch,
			repo.DefaultBranch,
			repo.Path,
			status,
		})
	}

	currentCursor := m.table.Cursor()

	m.table.SetRows(rows)

	tableHeight := m.calculateTableHeight(len(rows))
	m.table.SetHeight(tableHeight)

	if currentCursor < len(rows) {
		m.table.SetCursor(currentCursor)
	} else if len(rows) > 0 {
		m.table.SetCursor(len(rows) - 1)
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.windowWidth = msg.Width
		m.windowHeight = msg.Height

		m.table = updateTableWidth(m.table, msg.Width)

		tableHeight := m.calculateTableHeight(len(m.repos))
		m.table.SetHeight(tableHeight)

	case []branchUpdateMsg:
		for _, update := range msg {
			if update.index < len(m.branches) {
				m.branches[update.index] = update.currentBranch
			}
		}
		m.updateTableRows()
		m.table = updateTableWidth(m.table, m.windowWidth)

	case statusUpdateMsg:
		m.setStatusByPath(msg.path, msg.status)
		m.updateTableRows()
		m.table = updateTableWidth(m.table, m.windowWidth)

	case tea.KeyMsg:
		switch msg.String() {

		case "esc":
			if m.table.Focused() {
				m.table.Blur()
			} else {
				m.table.Focus()
			}

		case "q", "ctrl+c", "й", "ctrl+й":
			return m, tea.Quit

		case "enter":
			return m, tea.Batch(
				tea.Printf("Let's go to %s!", m.table.SelectedRow()[1]),
			)

		case "p", "P", "pull", "з", "З":
			if len(m.repos) > 0 && m.table.Cursor() < len(m.repos) {
				selectedRepo := m.getSelectedRepo()
				if selectedRepo != nil {
					m.wg.Add(1)
					go func(selectedRepo *config.RepoInfo, modelRef *model) {
						defer m.wg.Done()
						handlePullRepo(selectedRepo, modelRef)
					}(selectedRepo, &m)
				}
			}

		case "d", "D", "delete", "в", "В":
			if len(m.repos) > 0 && m.table.Cursor() < len(m.repos) {
				selectedRepo := m.getSelectedRepo()
				if selectedRepo != nil {
					m.wg.Add(1)
					go func() {
						defer m.wg.Done()
						_ = handleUnregisterRepo(selectedRepo.Path)
					}()

					currentCursor := m.table.Cursor()

					m.repos = m.removeRepo(selectedRepo.Path)

					if currentCursor < len(m.branches) {
						m.branches = append(m.branches[:currentCursor], m.branches[currentCursor+1:]...)
					}
					if currentCursor < len(m.statuses) {
						m.statuses = append(m.statuses[:currentCursor], m.statuses[currentCursor+1:]...)
					}

					m.updateTableRows()
					m.table = updateTableWidth(m.table, m.windowWidth)

					if len(m.repos) == 0 {
						m.table.SetCursor(0)
					} else if currentCursor >= len(m.repos) {
						m.table.SetCursor(len(m.repos) - 1)
					} else {
						m.table.SetCursor(currentCursor)
					}

					m.helpText = m.createHelpText()
					return m, tea.Printf("Unregistered: %s", selectedRepo.Name)
				}
			}
		}
	}

	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m model) View() string {
	tableFrameHeight := m.calculateTableFrameHeight()

	tableView := baseStyle.
		Width(m.windowWidth).
		Height(tableFrameHeight).
		Render(m.table.View())
	helpView := helpStyle.
		Width(m.windowWidth).
		Render(m.helpText)

	return lipgloss.NewStyle().
		Width(m.windowWidth).
		Height(m.windowHeight).
		Render(lipgloss.JoinVertical(lipgloss.Top, tableView, helpView))
}

func (m model) createTable() table.Model {
	columns := []table.Column{
		{Title: "NAME"},
		{Title: "CURRENT"},
		{Title: "BRANCH"},
		{Title: "PATH"},
		{Title: "STATUS"},
	}

	rows := []table.Row{}
	for i, repo := range m.repos {
		var currentBranch string
		var status string

		if i < len(m.branches) && m.branches[i] != "" {
			currentBranch = m.branches[i]
		} else {
			currentBranch = "..."
		}

		if i < len(m.statuses) && m.statuses[i] != "" {
			status = m.statuses[i]
		} else {
			status = "Ready"
		}

		rows = append(rows, table.Row{
			repo.Name,
			currentBranch,
			repo.DefaultBranch,
			repo.Path,
			status,
		})
	}

	tableHeight := m.calculateTableHeight(len(rows))

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(tableHeight),
		table.WithWidth(m.windowWidth-4),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	s.Selected = selectedRepoStyle
	t.SetStyles(s)

	if m.windowWidth > 0 {
		t = updateTableWidth(t, m.windowWidth)
	} else {
		t = updateTableWidth(t, 140)
	}

	return t
}

func DrawListTable(wg *sync.WaitGroup, repos []config.RepoInfo, isSilently bool) {
	isSilentlyLocal = isSilently

	sort.Slice(repos, func(i, j int) bool {
		return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name)
	})

	branches := make([]string, len(repos))
	statuses := make([]string, len(repos))

	for i := range statuses {
		statuses[i] = "Ready"
	}

	initialModel := model{
		wg:           wg,
		repos:        repos,
		initialRepos: make([]config.RepoInfo, len(repos)),
		branches:     branches,
		statuses:     statuses,
		windowWidth:  80,
		windowHeight: 24,
	}

	copy(initialModel.initialRepos, repos)

	initialModel.table = initialModel.createTable()
	initialModel.helpText = initialModel.createHelpText()

	p := tea.NewProgram(&initialModel, tea.WithAltScreen())

	initialModel.program = p

	if _, err := p.Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}

func (m model) createHelpText() string {
	helpText := "↑/↓: navigate • p: pull • d: unregister • q: quit"
	if len(m.repos) == 0 {
		helpText += " • No repositories"
	}
	return helpText
}

func (m model) calculateTableHeight(rowCount int) int {
	maxHeight := m.calculateTableFrameHeight()

	if maxHeight < minTableHeight {
		return minTableHeight
	}

	naturalHeight := rowCount + tableHeaderHeight
	if naturalHeight < minTableHeight {
		return minTableHeight
	}
	if naturalHeight > maxHeight {
		return maxHeight
	}

	return naturalHeight
}

func (m model) calculateTableFrameHeight() int {
	height := m.windowHeight - helpViewHeight
	if height < minTableHeight {
		return minTableHeight
	}

	return height
}

func handleUnregisterRepo(path string) error {
	configPath, err := config.GetConfigPath()
	if err != nil {
		return err
	}

	storage := config.NewStorageManager(configPath)
	if err := storage.Load(); err != nil {
		return err
	}

	err = storage.RemoveRepo(path)
	if err != nil {
		return err
	}

	if !isSilentlyLocal {
		go notifications.OSNotify(config.AppName, "Unregister", path)
	}

	return nil
}

func handlePullRepo(repo *config.RepoInfo, modelRef *model) error {
	var handleError error
	var pullResult string

	modelRef.sendStatusUpdate(repo.Path, "Checking branch...")

	defer func() {
		if handleError != nil {
			go notifications.OSNotify(
				config.AppName,
				fmt.Sprintf("%s pull failed", repo.Name),
				handleError.Error(),
			)
			modelRef.sendStatusUpdate(repo.Path, "Failed")

			go func() {
				time.Sleep(3 * time.Second)
				modelRef.sendStatusUpdate(repo.Path, "Ready")
			}()
		} else {
			go notifications.OSNotify(
				config.AppName,
				fmt.Sprintf("%s pull", repo.Name),
				pullResult,
			)
			modelRef.sendStatusUpdate(repo.Path, "Success")
			go updateRepoLastSync(repo.Path)

			go func() {
				time.Sleep(2 * time.Second)
				modelRef.sendStatusUpdate(repo.Path, "Ready")
			}()
		}
	}()

	currentBranch, err := git.GetCurrentBranch(repo.Path)
	if err != nil {
		handleError = fmt.Errorf("get current branch err %w", err)
		modelRef.sendStatusUpdate(repo.Path, "Branch check failed")
		return handleError
	}

	if currentBranch != repo.DefaultBranch {
		err := fmt.Errorf("current branch %s is not %s", currentBranch, repo.DefaultBranch)
		handleError = err
		modelRef.sendStatusUpdate(repo.Path, "Wrong branch")
		return handleError
	}

	modelRef.sendStatusUpdate(repo.Path, "Checking changes...")

	hasChanges, err := git.GitHasUncommitedChanges(repo.Path)
	if err != nil {
		handleError = fmt.Errorf("get changes err %w", err)
		modelRef.sendStatusUpdate(repo.Path, "Changes check failed")
		return handleError
	}
	if hasChanges {
		handleError = fmt.Errorf("repository has changes")
		modelRef.sendStatusUpdate(repo.Path, "Has uncommitted changes")
		return handleError
	}

	modelRef.sendStatusUpdate(repo.Path, "Pulling...")

	pullResult, handleError = git.GitPull(repo.Path)
	if handleError != nil {
		modelRef.sendStatusUpdate(repo.Path, "Pull failed")
	} else {
		modelRef.sendStatusUpdate(repo.Path, "Pull successful")
	}

	return handleError
}

func updateRepoLastSync(path string) error {
	configPath, err := config.GetConfigPath()
	if err != nil {
		return err
	}

	storage := config.NewStorageManager(configPath)
	if err := storage.Load(); err != nil {
		return err
	}
	err = storage.UpdateLastSync(path)
	return err
}
