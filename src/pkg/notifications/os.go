package notifications

import (
	_ "embed"

	"github.com/gen2brain/beeep"
)

//go:embed assets/info.png
var InfoIcon []byte

func OSNotifyURL(appName, title, body, openURL string) error {
	if err := customNotify(title, body, openURL); err == nil {
		return nil
	}

	beeep.AppName = appName
	return beeep.Notify(title, body, InfoIcon)
}
