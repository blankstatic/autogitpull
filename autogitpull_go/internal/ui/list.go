package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/lib"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Добавляем структуру для сообщения об обновлении ветки
type branchUpdateMsg struct {
	index         int
	currentBranch string
}

// Добавляем структуру для обновления статуса
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
	repos        []lib.RepoInfo
	initialRepos []lib.RepoInfo
	windowWidth  int
	windowHeight int
	branches     []string     // Кэш для текущих веток
	statuses     []string     // Кэш для статусов
	program      *tea.Program // Добавляем ссылку на программу
}

func (m model) Init() tea.Cmd {
	// Запускаем асинхронное получение веток при инициализации
	return tea.Batch(
		tea.WindowSize(),
		m.loadBranchesAsync(),
	)
}

// Команда для асинхронной загрузки веток
func (m model) loadBranchesAsync() tea.Cmd {
	return func() tea.Msg {
		var wg sync.WaitGroup
		branchChan := make(chan branchUpdateMsg, len(m.repos))

		// Запускаем горутины для каждого репозитория
		for i, repo := range m.repos {
			wg.Add(1)
			go func(index int, path string) {
				defer wg.Done()
				currentBranch, err := lib.GetCurrentBranch(path)
				if err != nil {
					currentBranch = "FAIL"
				}
				branchChan <- branchUpdateMsg{
					index:         index,
					currentBranch: currentBranch,
				}
			}(i, repo.Path)
		}

		// Закрываем канал после завершения всех горутин
		go func() {
			wg.Wait()
			close(branchChan)
		}()

		// Собираем результаты
		var updates []branchUpdateMsg
		for update := range branchChan {
			updates = append(updates, update)
		}

		return updates
	}
}

func updateTableWidth(t table.Model, totalWidth int) table.Model {
	columnPercentages := []int{15, 15, 15, 45, 10}

	borderWidth := 10
	availableWidth := totalWidth - borderWidth

	columns := t.Columns()
	updatedColumns := make([]table.Column, len(columns))
	for i, col := range columns {
		colWidth := (availableWidth * columnPercentages[i]) / 100
		updatedColumns[i] = table.Column{
			Title: col.Title,
			Width: colWidth,
		}
	}

	t.SetColumns(updatedColumns)
	t.SetWidth(availableWidth)
	return t
}

func (m model) getSelectedRepo() *lib.RepoInfo {
	if len(m.table.SelectedRow()) == 0 || m.table.Cursor() >= len(m.repos) {
		return nil
	}
	return &m.repos[m.table.Cursor()]
}

func (m model) removeRepo(path string) []lib.RepoInfo {
	var newRepos []lib.RepoInfo
	for _, repo := range m.repos {
		if repo.Path != path {
			newRepos = append(newRepos, repo)
		}
	}
	return newRepos
}

// Функция для установки статуса по path
func (m *model) setStatusByPath(path string, status string) {
	for i, repo := range m.repos {
		if repo.Path == path && i < len(m.statuses) {
			m.statuses[i] = status
			break
		}
	}
}

// Функция для отправки сообщения об обновлении статуса
func (m *model) sendStatusUpdate(path string, status string) {
	if m.program != nil {
		m.program.Send(statusUpdateMsg{
			path:   path,
			status: status,
		})
	}
}

// Обновляем только строки таблицы, не пересоздавая всю таблицу
func (m *model) updateTableRows() {
	rows := []table.Row{}
	for i, repo := range m.repos {
		var currentBranch string
		var status string

		// Используем кэшированное значение ветки, если оно есть
		if i < len(m.branches) && m.branches[i] != "" {
			currentBranch = m.branches[i]
		} else {
			// Если значения еще нет, показываем заглушку
			currentBranch = "..."
		}

		// Используем кэшированное значение статуса, если оно есть
		if i < len(m.statuses) && m.statuses[i] != "" {
			status = m.statuses[i]
		} else {
			// Статус по умолчанию
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

	// Сохраняем текущее состояние курсора
	currentCursor := m.table.Cursor()

	// Обновляем только строки
	m.table.SetRows(rows)

	// Обновляем высоту таблицы в соответствии с новым количеством строк
	tableHeight := m.calculateTableHeight(len(rows))
	m.table.SetHeight(tableHeight)

	// Восстанавливаем курсор, если он в пределах
	if currentCursor < len(rows) {
		m.table.SetCursor(currentCursor)
	} else if len(rows) > 0 {
		// Если курсор был за пределами, ставим на последний элемент
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
		m.table.SetWidth(msg.Width)

		// Обновляем высоту таблицы при изменении размера окна
		tableHeight := m.calculateTableHeight(len(m.repos))
		m.table.SetHeight(tableHeight)

	case []branchUpdateMsg:
		// Обрабатываем обновления веток
		for _, update := range msg {
			if update.index < len(m.branches) {
				m.branches[update.index] = update.currentBranch
			}
		}
		// Обновляем только строки таблицы
		m.updateTableRows()
		m.table.SetWidth(m.windowWidth)

	case statusUpdateMsg:
		// Обрабатываем обновление статуса
		m.setStatusByPath(msg.path, msg.status)
		// Обновляем только строки таблицы
		m.updateTableRows()
		m.table.SetWidth(m.windowWidth)

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
					go func(selectedRepo *lib.RepoInfo, modelRef *model) {
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

					// Сохраняем текущую позицию курсора
					currentCursor := m.table.Cursor()

					// Удаляем репозиторий
					m.repos = m.removeRepo(selectedRepo.Path)

					// Обновляем кэш веток и статусов
					if currentCursor < len(m.branches) {
						m.branches = append(m.branches[:currentCursor], m.branches[currentCursor+1:]...)
					}
					if currentCursor < len(m.statuses) {
						m.statuses = append(m.statuses[:currentCursor], m.statuses[currentCursor+1:]...)
					}

					// Обновляем строки таблицы (включая высоту)
					m.updateTableRows()
					m.table.SetWidth(m.windowWidth)

					// Корректируем позицию курсора после удаления
					if len(m.repos) == 0 {
						// Если репозиториев не осталось, оставляем курсор на 0
						m.table.SetCursor(0)
					} else if currentCursor >= len(m.repos) {
						// Если курсор был за пределами, ставим на последний элемент
						m.table.SetCursor(len(m.repos) - 1)
					} else {
						// Иначе оставляем на той же позиции
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
	tableView := baseStyle.Render(m.table.View())
	helpView := helpStyle.Render(m.helpText)

	return lipgloss.NewStyle().
		Width(m.windowWidth).
		Height(m.windowHeight).
		Render(tableView + "\n" + helpView)
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

		// Используем кэшированное значение ветки, если оно есть
		if i < len(m.branches) && m.branches[i] != "" {
			currentBranch = m.branches[i]
		} else {
			// Если значения еще нет, показываем заглушку
			currentBranch = "..."
		}

		// Используем кэшированное значение статуса, если оно есть
		if i < len(m.statuses) && m.statuses[i] != "" {
			status = m.statuses[i]
		} else {
			// Статус по умолчанию
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

func DrawListTable(wg *sync.WaitGroup, repos []lib.RepoInfo, isSilently bool) {
	isSilentlyLocal = isSilently

	sort.Slice(repos, func(i, j int) bool {
		return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name)
	})

	// Инициализируем кэш веток и статусов
	branches := make([]string, len(repos))
	statuses := make([]string, len(repos))

	// Устанавливаем начальные статусы
	for i := range statuses {
		statuses[i] = "Ready"
	}

	initialModel := model{
		wg:           wg,
		repos:        repos,
		initialRepos: make([]lib.RepoInfo, len(repos)),
		branches:     branches,
		statuses:     statuses,
		windowWidth:  80,
		windowHeight: 24,
	}

	copy(initialModel.initialRepos, repos)

	initialModel.table = initialModel.createTable()
	initialModel.helpText = initialModel.createHelpText()

	p := tea.NewProgram(&initialModel, tea.WithAltScreen()) // Используем указатель

	// Сохраняем ссылку на программу в модели
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
	minHeight := 4
	maxHeight := m.windowHeight - 3

	if maxHeight < minHeight {
		return minHeight
	}

	desiredHeight := rowCount + 1

	if desiredHeight < minHeight {
		return minHeight
	}
	if desiredHeight > maxHeight {
		return maxHeight
	}
	return desiredHeight
}

func handleUnregisterRepo(path string) error {
	configPath, err := lib.GetConfigPath()
	if err != nil {
		return err
	}

	storage := lib.NewStorageManager(configPath)
	if err := storage.Load(); err != nil {
		return err
	}

	err = storage.RemoveRepo(path)
	if err != nil {
		return err
	}

	if !isSilentlyLocal {
		go lib.ShowMessage(lib.AppName, "Unregister", path)
	}

	return nil
}

func handlePullRepo(repo *lib.RepoInfo, modelRef *model) error {
	var handleError error
	var pullResult string

	// Обновляем статус на "Checking branch..."
	modelRef.sendStatusUpdate(repo.Path, "Checking branch...")

	defer func() {
		if handleError != nil {
			go lib.ShowMessage(
				lib.AppName,
				fmt.Sprintf("%s pull failed", repo.Name),
				handleError.Error(),
			)
			// Обновляем статус на "Failed"
			modelRef.sendStatusUpdate(repo.Path, "Failed")

			// Через 3 секунды возвращаем статус "Ready"
			go func() {
				time.Sleep(3 * time.Second)
				modelRef.sendStatusUpdate(repo.Path, "Ready")
			}()
		} else {
			go lib.ShowMessage(
				lib.AppName,
				fmt.Sprintf("%s pull", repo.Name),
				pullResult,
			)
			// Обновляем статус на "Success"
			modelRef.sendStatusUpdate(repo.Path, "Success")
			go updateRepoLastSync(repo.Path)

			// Через 2 секунды возвращаем статус "Ready"
			go func() {
				time.Sleep(2 * time.Second)
				modelRef.sendStatusUpdate(repo.Path, "Ready")
			}()
		}
	}()

	currentBranch, err := lib.GetCurrentBranch(repo.Path)
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

	// Обновляем статус на "Checking changes..."
	modelRef.sendStatusUpdate(repo.Path, "Checking changes...")

	hasChanges, err := lib.GitHasUncommitedChanges(repo.Path)
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

	// Обновляем статус на "Pulling..."
	modelRef.sendStatusUpdate(repo.Path, "Pulling...")

	pullResult, handleError = lib.GitPull(repo.Path)
	if handleError != nil {
		modelRef.sendStatusUpdate(repo.Path, "Pull failed")
	} else {
		modelRef.sendStatusUpdate(repo.Path, "Pull successful")
	}

	return handleError
}

func updateRepoLastSync(path string) error {
	configPath, err := lib.GetConfigPath()
	if err != nil {
		return err
	}

	storage := lib.NewStorageManager(configPath)
	if err := storage.Load(); err != nil {
		return err
	}
	err = storage.UpdateLastSync(path)
	return err
}
