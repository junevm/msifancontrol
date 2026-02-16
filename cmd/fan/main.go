package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/junevm/msifancontrol/internal/config"
	"github.com/junevm/msifancontrol/internal/fan"
	"github.com/junevm/msifancontrol/internal/setup"
	"github.com/junevm/msifancontrol/internal/ui"
)

// Version is the current version of the application.
// This is set at build time via -ldflags.
var Version = "dev"

// main is the entry point of the application.
func main() {
	// 0. Auto-Elevation
	// If we are not running as root, we re-execute ourselves with sudo.
	if os.Geteuid() != 0 {
		// If the user is just asking for the version, we don't need root.
		for _, arg := range os.Args[1:] {
			if arg == "--version" || arg == "-v" {
				fmt.Printf("msifancontrol version %s\n", Version)
				return
			}
		}

		// Get the path to the current executable
		exe, err := os.Executable()
		if err != nil {
			log.Fatalf("Failed to get executable path: %v", err)
		}

		// Prepare the command: sudo <executable> <args>
		// We pass all original arguments to the new process.
		cmd := exec.Command("sudo", append([]string{exe}, os.Args[1:]...)...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		// Run the command and wait for it to finish.
		if err := cmd.Run(); err != nil {
			// If the user cancelled the password prompt or sudo failed.
			log.Fatalf("Failed to run as root: %v", err)
		}
		
		// Exit the parent process successfully.
		return
	}

	// 1. Parse Command Line Arguments
	// We allow the user to pass a "--cli" flag to run without the graphical interface.
	// This is useful for scripts or startup tasks.
	cliMode := flag.Bool("cli", false, "Run in CLI mode (apply config and exit)")
	setupMode := flag.Bool("setup", false, "Run setup to build/install ec_sys module")
	versionMode := flag.Bool("version", false, "Display version and exit")
	shortVersionMode := flag.Bool("v", false, "Display version and exit")
	flag.Parse()

	// 2. Handle Version Mode
	if *versionMode || *shortVersionMode {
		fmt.Printf("msifancontrol version %s\n", Version)
		return
	}

	// 2. Handle Setup Mode
	if *setupMode {
		if err := setup.RunFullSetup(nil); err != nil {
			log.Fatalf("Setup failed: %v", err)
		}
		fmt.Println("Setup completed successfully.")
		return
	}

	// 3. Check Environment (Auto-Setup Check)
	// We check if the kernel module is ready.
	// If not, we'll pass this info to the UI so it can guide the user.
	needsSetup := false
	if err := setup.CheckAndSetup(); err != nil {
		needsSetup = true
	}

	// 4. Load Configuration
	// We try to read settings from 'config.json'.
	// If that fails (e.g., file doesn't exist), we use safe default settings.
	cfg, err := config.Load()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		cfg = config.DefaultConfig()
	}

	// 5. Handle CLI Mode
	// If the user ran with "--cli", we just apply the settings and quit.
	if *cliMode {
		if needsSetup {
			log.Fatal("Error: ec_sys module missing. Run 'sudo fan --setup' first.")
		}
		fmt.Println("Applying fan profile...")
		if err := fan.ApplyProfile(cfg); err != nil {
			log.Fatalf("Error applying profile: %v", err)
		}
		fmt.Println("Profile applied successfully.")
		return
	}

	// 6. Handle GUI Mode (Default)
	
	// Start the User Interface.
	// This hands over control to the Bubble Tea framework in 'internal/ui/ui.go'.
	if err := ui.Run(cfg, needsSetup); err != nil {
		log.Fatalf("Error running UI: %v", err)
	}
}
