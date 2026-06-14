package ui

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/db"
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
	defaultTableWidth            = 140
	loadingBranchText            = "..."
	readyStatusText              = "Ready"
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
	currentCursor := m.table.Cursor()
	rows := m.tableRows()

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
	rows := m.tableRows()
	tableHeight := m.calculateTableHeight(len(rows))

	t := table.New(
		table.WithColumns(tableColumns()),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(tableHeight),
		table.WithWidth(m.windowWidth-4),
	)

	t.SetStyles(tableStyles())

	if m.windowWidth > 0 {
		t = updateTableWidth(t, m.windowWidth)
	} else {
		t = updateTableWidth(t, defaultTableWidth)
	}

	return t
}

func tableColumns() []table.Column {
	return []table.Column{
		{Title: "NAME"},
		{Title: "CURRENT"},
		{Title: "BRANCH"},
		{Title: "PATH"},
		{Title: "STATUS"},
	}
}

func tableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	s.Selected = selectedRepoStyle
	return s
}

func (m model) tableRows() []table.Row {
	rows := []table.Row{}
	for i, repo := range m.repos {
		rows = append(rows, table.Row{
			repo.Name,
			m.branchText(i),
			repo.DefaultBranch,
			repo.Path,
			m.statusText(i),
		})
	}
	return rows
}

func (m model) branchText(index int) string {
	if index < len(m.branches) && m.branches[index] != "" {
		return m.branches[index]
	}
	return loadingBranchText
}

func (m model) statusText(index int) string {
	if index < len(m.statuses) && m.statuses[index] != "" {
		return m.statuses[index]
	}
	return readyStatusText
}

func DrawListTable(wg *sync.WaitGroup, repos []config.RepoInfo, isSilently bool) {
	isSilentlyLocal = isSilently

	sort.Slice(repos, func(i, j int) bool {
		return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name)
	})

	initialModel := newListModel(wg, repos)
	initialModel.table = initialModel.createTable()
	initialModel.helpText = initialModel.createHelpText()

	p := tea.NewProgram(&initialModel, tea.WithAltScreen())

	initialModel.program = p

	if _, err := p.Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}

func newListModel(wg *sync.WaitGroup, repos []config.RepoInfo) model {
	branches := make([]string, len(repos))
	statuses := make([]string, len(repos))

	for i := range statuses {
		statuses[i] = readyStatusText
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
	return initialModel
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
		notifyAsync("Unregister", path, "")
	}

	return nil
}

func handlePullRepo(repo *config.RepoInfo, modelRef *model) error {
	var handleError error
	var pullResult string
	var updateStore *db.Store
	var updateID int64

	modelRef.sendStatusUpdate(repo.Path, "Checking branch...")

	updatesDBPath, err := config.GetUpdatesDBPath()
	if err != nil {
		slog.Error("failed to get updates database path", slog.String("repo", repo.Name), slog.String("err", err.Error()))
	} else {
		updateStore, err = db.Open(updatesDBPath)
		if err != nil {
			slog.Error("failed to open updates database", slog.String("repo", repo.Name), slog.String("err", err.Error()))
		} else {
			defer updateStore.Close()
			updateID, err = updateStore.BeginUpdate(repo.Path, repo.Name)
			if err != nil {
				slog.Error("failed to record update start", slog.String("repo", repo.Name), slog.String("err", err.Error()))
			}
		}
	}

	defer func() {
		if updateStore != nil && updateID > 0 {
			if err := updateStore.FinishUpdate(updateID, pullResult, handleError); err != nil {
				slog.Error("failed to record update result", slog.String("repo", repo.Name), slog.String("err", err.Error()))
			}
		}

		notifyURL := "http://localhost:9009/repo?path=" + url.QueryEscape(repo.Path)
		if handleError != nil {
			notifyAsync(
				fmt.Sprintf("%s pull failed", repo.Name),
				handleError.Error(),
				notifyURL,
			)
			if db.IsSkippedPullError(handleError.Error()) {
				modelRef.sendStatusUpdate(repo.Path, "Skipped")
			} else {
				modelRef.sendStatusUpdate(repo.Path, "Failed")
			}

			go func() {
				time.Sleep(3 * time.Second)
				modelRef.sendStatusUpdate(repo.Path, "Ready")
			}()
		} else {
			notifyAsync(
				fmt.Sprintf("%s pull", repo.Name),
				pullResult,
				notifyURL,
			)
			modelRef.sendStatusUpdate(repo.Path, "Success")
			go func() {
				if err := updateRepoLastSync(repo.Path); err != nil {
					slog.Error("failed to update repo last sync", slog.String("repo", repo.Name), slog.String("err", err.Error()))
				}
			}()

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

func notifyAsync(title, body, openURL string) {
	go func() {
		if err := notifications.OSNotifyURL(config.AppName, title, body, openURL); err != nil {
			slog.Error("failed to send notification", slog.String("title", title), slog.String("err", err.Error()))
		}
	}()
}
