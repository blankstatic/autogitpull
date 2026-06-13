package logic

import (
	"fmt"
	"os"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/service"
	"github.com/spf13/cobra"
)

const serviceIntervalMin = 30

func ServiceCommandHandler(cmd *cobra.Command, args []string) {
	runServiceCommand(args[0])
}

func runServiceCommand(command string) {
	configPath, err := config.GetConfigPath()
	if err != nil {
		exitServiceError("Error getting config path", err)
	}

	interval := time.Duration(serviceIntervalMin) * time.Minute
	manager := service.New(configPath, interval)

	switch command {
	case "install":
		err := manager.Install()
		if err != nil {
			exitServiceError("Install failed", err)
		}
		fmt.Println("OK: service installed")

	case "start":
		err := manager.Start()
		if err != nil {
			exitServiceError("Start failed", err)
		}
		fmt.Println("OK: service started")

	case "stop":
		err := manager.Stop()
		if err != nil {
			exitServiceError("Stop failed", err)
		}
		fmt.Println("OK: service stopped")

	case "uninstall":
		err := manager.Uninstall()
		if err != nil {
			exitServiceError("Uninstall failed", err)
		}
		fmt.Println("OK: service uninstalled")

	case "status":
		status, err := manager.Status()
		if err != nil {
			exitServiceError("Status check failed", err)
		}
		fmt.Printf("Service status: %s\n", status)

	default:
		fmt.Printf("Unknown service command: %s\n", command)
		printServiceUsage()
		os.Exit(1)
	}
}

func exitServiceError(message string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", message, err)
	os.Exit(1)
}

func printServiceUsage() {
	fmt.Println(`Usage: autogitpull service <command>

Commands:
    install    - Install the launchd service
    start      - Start the service
    stop       - Stop the service
    uninstall  - Uninstall the service
    status     - Check service status`)
}
