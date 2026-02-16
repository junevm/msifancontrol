package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/junevm/msifancontrol/internal/config"
	"github.com/junevm/msifancontrol/internal/fan"
	"github.com/junevm/msifancontrol/internal/setup"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------
// 🎨 AESTHETICS: Dreamy 90s Vaporwave Palette
// ---------------------------------------------------------
// We use a library called "Lipgloss" to style our text.
// Think of it like CSS for the terminal.

var (
	// Colors: Defining our color palette variables.
	colorPink   = lipgloss.Color("#FF71CE")
	colorCyan   = lipgloss.Color("#01CDFE")
	colorPurple = lipgloss.Color("#B967FF")
	colorYellow = lipgloss.Color("#FFFFB6")
	colorDark   = lipgloss.Color("#1A1A2E")
	colorGray   = lipgloss.Color("#6E6E80")

	// Styles: Defining reusable styles for different parts of the UI.
	
	// The main container for the application.
	appStyle = lipgloss.NewStyle().
			Padding(1, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPurple).
			Background(colorDark)


	// The title bar at the top.
	titleStyle = lipgloss.NewStyle().
			Foreground(colorYellow).
			Background(colorPurple).
			Padding(0, 1).
			Bold(true).
			MarginBottom(1)

	// Headers for sections like "SYSTEM STATUS".
	headerStyle = lipgloss.NewStyle().
			Foreground(colorCyan).
			Bold(true).
			MarginBottom(1)

	// Labels for statistics (e.g., "CPU Temp").
	statLabelStyle = lipgloss.NewStyle().
			Foreground(colorPink).
			Width(12)

	// Values for statistics (e.g., "45°C").
	statValueStyle = lipgloss.NewStyle().
			Foreground(colorYellow).
			Bold(true)

	// List Styles for the profile menu.
	itemStyle = lipgloss.NewStyle().
			PaddingLeft(2).
			Foreground(colorCyan)

	// The currently selected item in the menu.
	selectedItemStyle = lipgloss.NewStyle().
				PaddingLeft(2).
				Foreground(colorDark).
				Background(colorPink).
				Bold(true)

	// Messages that appear when you apply a profile.
	statusMessageStyle = lipgloss.NewStyle().
				Foreground(colorYellow).
				Italic(true)

	// The help text at the bottom.
	helpStyle = lipgloss.NewStyle().
			Foreground(colorGray).
			MarginTop(1)
)

// ---------------------------------------------------------
// 🧠 MODEL
// ---------------------------------------------------------
// The "Model" holds the entire state of our application.
// In the Bubble Tea framework (based on The Elm Architecture),
// the Model is the single source of truth.

type tickMsg time.Time // A message type for our periodic timer.
type setupFinishedMsg struct{ err error } // Message when setup completes
type setupLogMsg string                   // Message for setup progress logs

type model struct {
	config       config.Config   // The current application configuration.
	spinner      spinner.Model   // The little loading animation.
	cursor       int             // Which menu item is currently selected (0-3).
	profiles     []string        // List of available profile names.
	cpuTemp      int             // Current CPU temperature.
	gpuTemp      int             // Current GPU temperature.
	cpuRpm       int             // Current CPU fan speed.
	gpuRpm       int             // Current GPU fan speed.
	statusMsg    string          // Message to display to the user (e.g., "Applied!").
	err          error           // Any error that occurred.
	width        int             // Terminal width.
	height       int             // Terminal height.
	needsSetup   bool            // If true, we show the setup screen.
	setupRunning bool            // If true, setup is currently running.
	setupErr     error           // Error from the setup process.
	setupLog     string          // Current log message from setup.
	fullLog      string          // Full log history
	setupChan    chan string     // Channel for setup logs.
	viewport     viewport.Model  // Viewport for scrolling logs
}

// InitialModel sets up the starting state of the application.
func InitialModel(cfg config.Config, needsSetup bool) model {
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(colorPink)

	vp := viewport.New(0, 0)
	vp.Style = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorGray).
		Padding(0, 1)

	return model{
		config:     cfg,
		spinner:    s,
		viewport:   vp,
		profiles:   []string{"Auto", "Basic", "Advanced", "Cooler Booster"},
		cursor:     cfg.Profile - 1, // Set cursor to the currently active profile.
		needsSetup: needsSetup,
	}
}

// Init is the first function called by Bubble Tea.
// It starts the spinner animation and the data refresh timer.
func (m model) Init() tea.Cmd {
	// If setup is needed, we only start the spinner, not the hardware polling.
	if m.needsSetup {
		return m.spinner.Tick
	}
	return tea.Batch(
		m.spinner.Tick,
		tickCmd(),
	)
}

// Update is the brain of the application.
// It receives "Messages" (events) and returns a new Model and a Command.
// Messages can be key presses, timer ticks, or window resizes.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	
	// The user resized the terminal window.
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width - 20
		m.viewport.Height = msg.Height - 10

	// The user pressed a key.
	case tea.KeyMsg:
		switch msg.String() {
		// Quit the application.
		case "ctrl+c", "q":
			return m, tea.Quit

		// Move cursor up.
		case "up", "k":
			if m.needsSetup {
				return m, nil
			}
			if m.cursor > 0 {
				m.cursor--
			} else {
				m.cursor = len(m.profiles) - 1 // Wrap around to bottom.
			}

		// Move cursor down.
		case "down", "j":
			if m.needsSetup {
				return m, nil
			}
			if m.cursor < len(m.profiles)-1 {
				m.cursor++
			} else {
				m.cursor = 0 // Wrap around to top.
			}

		// Select the current profile OR Start Setup.
		case "enter", " ":
			if m.needsSetup {
				if !m.setupRunning {
					m.setupRunning = true
					m.setupErr = nil
					m.setupLog = "Initializing..."
					m.fullLog = "Initializing setup...\n"
					m.viewport.SetContent(m.fullLog)
					m.setupChan = make(chan string, 10)
					return m, tea.Batch(
						runSetupCmd(m.setupChan),
						waitForSetupLog(m.setupChan),
					)
				}
				return m, nil
			}

			m.config.Profile = m.cursor + 1
			// Apply the profile to the hardware.
			if err := fan.ApplyProfile(m.config); err != nil {
				m.statusMsg = fmt.Sprintf("⚡ Error: %v", err)
			} else {
				m.statusMsg = fmt.Sprintf("✨ Applied: %s", m.profiles[m.cursor])
				// Save the new choice to config.json.
				if err := config.Save(m.config); err != nil {
					m.statusMsg = fmt.Sprintf("⚠️ Saved failed: %v", err)
				}
			}

		// Re-run setup manually
		case "R":
			if !m.needsSetup {
				m.needsSetup = true
				m.setupErr = nil
				return m, nil
			}
		}

	// Setup log received
	case setupLogMsg:
		m.setupLog = string(msg)
		m.fullLog += string(msg) + "\n"
		m.viewport.SetContent(m.fullLog)
		m.viewport.GotoBottom()
		return m, waitForSetupLog(m.setupChan)

	// Setup finished
	case setupFinishedMsg:
		m.setupRunning = false
		if msg.err != nil {
			m.setupErr = msg.err
		} else {
			m.needsSetup = false
			// Start polling now that setup is done
			return m, tickCmd()
		}

	// The spinner animation updated.
	case spinner.TickMsg:
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	// Our custom timer ticked (every 1 second).
	case tickMsg:
		// If we still need setup, don't poll hardware
		if m.needsSetup {
			return m, nil
		}
		var err error
		// Refresh temperatures and RPMs from the hardware.
		m.cpuTemp, m.gpuTemp, err = fan.GetTemps(m.config)
		if err != nil {
			m.err = err
		}
		m.cpuRpm, m.gpuRpm, err = fan.GetRPMs(m.config)
		if err != nil {
			m.err = err
		}
		// Schedule the next tick.
		cmds = append(cmds, tickCmd())
	}

	return m, tea.Batch(cmds...)
}

// ---------------------------------------------------------
// 👁️ VIEW
// ---------------------------------------------------------
// View renders the UI based on the current Model.
// It returns a string that represents what should be printed to the terminal.

func (m model) View() string {
	// 1. Title
	title := titleStyle.Render(" 💿 MSI FAN CONTROL 95 ")

	// 2. Setup Screen (if needed)
	if m.needsSetup {
		var content string
		if m.setupRunning {
			content = fmt.Sprintf("\n\n   %s Installing kernel module...\n\n%s", m.spinner.View(), m.viewport.View())
		} else if m.setupErr != nil {
			content = fmt.Sprintf("%s\n\n   ❌ Setup Failed:\n   %v\n\n   Press [Enter] to retry or [q] to quit.", m.viewport.View(), m.setupErr)
		} else {
			content = "\n\n   ⚠️  Kernel Module Setup\n\n   The 'ec_sys' module is required to control fans.\n   We can build and install it for you automatically.\n\n   Press [Enter] to install."
		}

		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPink).
			Padding(1, 3).
			Align(lipgloss.Center).
			Render(content)

		return appStyle.Render(lipgloss.JoinVertical(lipgloss.Center, title, box))
	}

	// 3. Stats Panel (Left side)
	statsContent := lipgloss.JoinVertical(lipgloss.Left,
		headerStyle.Render("SYSTEM STATUS"),
		renderStat("CPU Temp", fmt.Sprintf("%d°C", m.cpuTemp)),
		renderStat("GPU Temp", fmt.Sprintf("%d°C", m.gpuTemp)),
		renderStat("CPU RPM", fmt.Sprintf("%d", m.cpuRpm)),
		renderStat("GPU RPM", fmt.Sprintf("%d", m.gpuRpm)),
		"",
		m.spinner.View()+" Monitoring...",
	)
	statsBox := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colorCyan).
		Padding(1).
		Width(30).
		Render(statsContent)

	// 4. Profiles Panel (Right side)
	var profileItems []string
	profileItems = append(profileItems, headerStyle.Render("SELECT PROFILE"))

	for i, profile := range m.profiles {
		if m.cursor == i {
			// Highlight the selected item.
			profileItems = append(profileItems, selectedItemStyle.Render(fmt.Sprintf("➤ %s", strings.ToUpper(profile))))
		} else {
			profileItems = append(profileItems, itemStyle.Render(profile))
		}
	}

	// Add status message at bottom of profiles
	if m.statusMsg != "" {
		profileItems = append(profileItems, "\n"+statusMessageStyle.Render(m.statusMsg))
	}

	profilesBox := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colorPink).
		Padding(1).
		Width(30).
		Height(lipgloss.Height(statsBox)). // Match height of stats box.
		Render(lipgloss.JoinVertical(lipgloss.Left, profileItems...))

	// 5. Layout: Put Stats and Profiles side-by-side.
	// If the terminal is too narrow, stack them vertically.
	var mainContent string
	if m.width > 0 && m.width < 70 {
		mainContent = lipgloss.JoinVertical(lipgloss.Left, statsBox, profilesBox)
	} else {
		mainContent = lipgloss.JoinHorizontal(lipgloss.Top, statsBox, profilesBox)
	}

	// 6. Footer: Help text.
	footer := helpStyle.Render("keys: ↑/↓ select • enter apply • R reinstall driver • q quit")

	// Combine all parts vertically.
	ui := lipgloss.JoinVertical(lipgloss.Center,
		title,
		mainContent,
		footer,
	)

	// Center the entire UI in the terminal
	return appStyle.Render(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, ui))
}

// Helper function to render a single statistic line.
func renderStat(label, value string) string {
	return lipgloss.JoinHorizontal(lipgloss.Bottom,
		statLabelStyle.Render(label),
		statValueStyle.Render(value),
	)
}

// tickCmd creates a command that waits for 1 second and then sends a tickMsg.
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// runSetupCmd runs the setup process in the background.
func runSetupCmd(ch chan string) tea.Cmd {
	return func() tea.Msg {
		defer close(ch)
		err := setup.RunFullSetup(ch)
		return setupFinishedMsg{err: err}
	}
}

// waitForSetupLog waits for the next log message from the channel.
func waitForSetupLog(ch chan string) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil // Channel closed
		}
		return setupLogMsg(msg)
	}
}

// Run starts the Bubble Tea program.
func Run(cfg config.Config, needsSetup bool) error {
	// tea.WithAltScreen() switches to the alternate terminal buffer,
	// so when you quit, the terminal is restored to its previous state.
	p := tea.NewProgram(InitialModel(cfg, needsSetup), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

