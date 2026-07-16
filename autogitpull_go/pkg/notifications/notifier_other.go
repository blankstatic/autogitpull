//go:build !darwin && !linux

package notifications

import "errors"

func customNotify(title, body, openURL string) error {
	return errors.New("custom notifier is only available on macOS")
}
