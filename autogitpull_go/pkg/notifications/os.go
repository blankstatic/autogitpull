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
	if err := customNotify(title, body); err == nil {
		return nil
	}

	beeep.AppName = appName
	return beeep.Notify(title, body, InfoIcon)
}
