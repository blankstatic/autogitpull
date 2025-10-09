package notifications

import (
	_ "embed"

	"github.com/gen2brain/beeep"
)

//go:embed assets/info.png
var InfoIcon []byte

//go:embed assets/warning.png
var WarningIcon []byte

func OSNotify(appName, title, body string) error {
	beeep.AppName = appName
	err := beeep.Notify(title, body, InfoIcon)
	return err
}
