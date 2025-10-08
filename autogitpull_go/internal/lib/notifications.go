package lib

import (
	_ "embed"

	"github.com/gen2brain/beeep"
)

func ShowMessage(appName, title, body string) error {
	beeep.AppName = appName
	err := beeep.Notify(title, body, InfoIcon)
	return err
}
