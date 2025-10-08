package cmd

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/lib"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/service"
	"github.com/spf13/cobra"
)

var (
	serviceInterval int
)

var serviceCmd = &cobra.Command{
	Use:   "service [install|start|stop|uninstall|status]",
	Short: "Manage the auto-pull macOS launchd service",
	Long:  `Install, start, stop, uninstall or check status of the background launchd service on macOS`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runServiceCommand(args[0])
	},
}

func init() {
	serviceCmd.Flags().IntVarP(&serviceInterval, "interval", "i", 30, "Interval in minutes for auto-pull")
	rootCmd.AddCommand(serviceCmd)
}

func runServiceCommand(command string) {
	configPath, err := lib.GetConfigPath()
	if err != nil {
		log.Fatal("Error getting config path:", err)
	}

	interval := time.Duration(serviceInterval) * time.Minute
	manager := service.New(configPath, interval)

	switch command {
	case "install":
		err := manager.Install()
		if err != nil {
			log.Fatal("Install failed:", err)
		}
		fmt.Println("✅ Service installed successfully")

	case "start":
		err := manager.Start()
		if err != nil {
			log.Fatal("Start failed:", err)
		}
		fmt.Println("✅ Service started successfully")

	case "stop":
		err := manager.Stop()
		if err != nil {
			log.Fatal("Stop failed:", err)
		}
		fmt.Println("✅ Service stopped successfully")

	case "uninstall":
		err := manager.Uninstall()
		if err != nil {
			log.Fatal("Uninstall failed:", err)
		}
		fmt.Println("✅ Service uninstalled successfully")

	case "status":
		status, err := manager.Status()
		if err != nil {
			log.Fatal("Status check failed:", err)
		}
		fmt.Printf("📊 Service status: %s\n", status)

	default:
		fmt.Printf("Unknown service command: %s\n", command)
		printServiceUsage()
		os.Exit(1)
	}
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
