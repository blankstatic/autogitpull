package logic

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/fs"
	"github.com/mutagen-io/mutagen/pkg/daemon"
	"github.com/spf13/cobra"
)

func GetIsSilentlyValue(cmd *cobra.Command) bool {
	isSilently, _ := cmd.Flags().GetBool("silently")
	return isSilently
}

func AcquireLock(dataDir string) (*daemon.Lock, error) {
	// Attempt to acquire the daemon lock
	// defer lock.Release()

	os.Setenv("MUTAGEN_DATA_DIRECTORY", dataDir)

	lock, err := daemon.AcquireLock()
	if err != nil {
		err = fmt.Errorf("unable to acquire daemon lock: %w", err)
		return nil, err
	}
	return lock, err
}

func ExitAtError(err error) {
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func GetSignalsChannel() chan os.Signal {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan,
		os.Interrupt,    // Ctrl+C
		syscall.SIGTERM, // k8s, docker stop
		syscall.SIGINT,
		syscall.SIGHUP,
	)
	return sigChan
}

func AddRepository(path string) {
	fmt.Println("add repo", path)
}

func ProcessArgsAsPaths(args []string, processPathFunc func(string) error) {
	if len(args) == 0 {
		currentDir, err := fs.GetCurrentDirectory()
		if err != nil {
			fmt.Println(err)
			os.Exit(0)
		}
		args = []string{currentDir}
	}

	for _, path := range args {
		absCurrentDir, err := fs.PathToAbsPath(path)
		if err != nil {
			fmt.Println(err)
			continue
		}

		err = processPathFunc(absCurrentDir)
		if err != nil {
			fmt.Println(err)
			continue
		}
	}
}
