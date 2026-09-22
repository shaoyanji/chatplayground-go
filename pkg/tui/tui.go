package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/shaoyanji/chatplayground-go/pkg/api"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4")).
			MarginBottom(1)

	modelTagStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#04B575")).
			Background(lipgloss.Color("#2E3440")).
			Padding(0, 1)

	userPrefixStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#04B575"))

	assistantPrefixStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#00D7D7"))
)

type errMsg error
type doneMsg string

type Model struct {
	viewport     viewport.Model
	textInput    textinput.Model
	glamourRend  *glamour.TermRenderer
	client       *api.Client
	currentModel string
	models       []string
	modelIndex   int
	history      []string
	historyIdx   int
	conversation string
	streaming    bool
	err          error
}

func InitialModel() Model {
	ti := textinput.New()
	ti.Placeholder = "Type prompt... ([Tab] Switch model, /new New thread, [Esc] Quit)"
	ti.Focus()
	ti.CharLimit = 2048
	ti.Width = 80

	vp := viewport.New(80, 20)
	vp.SetContent("Welcome to ChatPlayground TUI!\nMulti-turn conversation persistence enabled. Context is retained in the same thread.\nCommands: /new (start new thread), /clear (reset conversation).\n\n")

	r, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)

	models := []string{
		"gemini-3.8-flash-l",
		"claude-sonnet-5-l",
		"glm-5.3-flash",
		"deepseek-v4-pro",
		"deepseek-v4-flash",
		"deepseek-r1",
		"grok-4.6",
	}

	return Model{
		viewport:     vp,
		textInput:    ti,
		glamourRend:  r,
		client:       api.NewClient(),
		currentModel: models[0],
		models:       models,
		modelIndex:   0,
		history:      []string{},
		historyIdx:   -1,
		conversation: "",
	}
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	m.textInput, tiCmd = m.textInput.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit

		case tea.KeyTab:
			m.modelIndex = (m.modelIndex + 1) % len(m.models)
			m.currentModel = m.models[m.modelIndex]
			return m, nil

		case tea.KeyUp:
			if len(m.history) > 0 && m.historyIdx > 0 {
				m.historyIdx--
				m.textInput.SetValue(m.history[m.historyIdx])
				m.textInput.SetCursor(len(m.textInput.Value()))
			}
			return m, nil

		case tea.KeyDown:
			if len(m.history) > 0 && m.historyIdx < len(m.history)-1 {
				m.historyIdx++
				m.textInput.SetValue(m.history[m.historyIdx])
				m.textInput.SetCursor(len(m.textInput.Value()))
			} else if m.historyIdx == len(m.history)-1 {
				m.historyIdx = len(m.history)
				m.textInput.Reset()
			}
			return m, nil

		case tea.KeyEnter:
			input := strings.TrimSpace(m.textInput.Value())
			if input == "" || m.streaming {
				return m, nil
			}

			if input == "/clear" || input == "/new" || input == "/reset" {
				m.conversation = ""
				m.client.ResetSession()
				m.viewport.SetContent("Conversation cleared and new thread started.\n\n")
				m.textInput.Reset()
				return m, nil
			}

			m.history = append(m.history, input)
			m.historyIdx = len(m.history)
			m.textInput.Reset()
			m.streaming = true

			// Append user message safely (no Builder copy panic)
			m.conversation += fmt.Sprintf("\n%s %s\n\n", userPrefixStyle.Render("You:"), input)
			m.viewport.SetContent(m.renderContent(m.conversation))
			m.viewport.GotoBottom()

			// Launch streaming query
			currentModel := m.currentModel
			return m, func() tea.Msg {
				fullText, err := m.client.StreamQuery(currentModel, input, "", nil)
				if err != nil {
					return errMsg(err)
				}
				return doneMsg(fullText)
			}
		}

	case doneMsg:
		m.streaming = false
		m.conversation += fmt.Sprintf("%s\n%s\n\n", assistantPrefixStyle.Render(m.currentModel+":"), string(msg))
		m.viewport.SetContent(m.renderContent(m.conversation))
		m.viewport.GotoBottom()
		return m, nil

	case errMsg:
		m.streaming = false
		m.conversation += fmt.Sprintf("\n\033[1;31mError: %v\033[0m\n\n", msg)
		m.viewport.SetContent(m.conversation)
		m.viewport.GotoBottom()
		return m, nil

	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 6
		m.textInput.Width = msg.Width - 4
		if m.glamourRend != nil {
			m.glamourRend, _ = glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(msg.Width-2))
		}
	}

	return m, tea.Batch(tiCmd, vpCmd)
}

func (m Model) renderContent(in string) string {
	if m.glamourRend != nil {
		rendered, err := m.glamourRend.Render(in)
		if err == nil {
			return rendered
		}
	}
	return in
}

func (m Model) View() string {
	threadBadge := ""
	if m.client != nil && m.client.ChatID != "" {
		id := m.client.ChatID
		if len(id) > 16 {
			id = id[:16] + "..."
		}
		threadBadge = fmt.Sprintf("  |  Thread: %s", titleStyle.Render(id))
	}

	status := fmt.Sprintf(" %s Model: %s%s  |  [Tab] Switch Model  |  /new Reset  |  [Esc] Quit ",
		titleStyle.Render("ChatPlayground"),
		modelTagStyle.Render(m.currentModel),
		threadBadge,
	)

	return fmt.Sprintf(
		"%s\n\n%s\n\n%s",
		status,
		m.viewport.View(),
		m.textInput.View(),
	)
}

func Start() error {
	p := tea.NewProgram(InitialModel(), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
