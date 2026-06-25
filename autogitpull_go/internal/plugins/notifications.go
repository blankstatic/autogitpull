package plugins

import (
	"fmt"

	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/notifications"
)

const NotificationsID = "notifications"

var notifyURL = notifications.OSNotifyURL

func notificationPlugin() Definition {
	return Definition{
		ID:          NotificationsID,
		Name:        "Notifications",
		Description: "Send OS notifications when a pull brings new changes.",
		DefaultOn:   true,
		DefaultConfig: map[string]string{
			"title_prefix": "Pulled",
		},
		RunOnNoChange: true,
		Fields: []Field{
			{Key: "title_prefix", Label: "Title prefix", Type: "text"},
		},
		Run: func(ctx Context) error {
			if !ctx.Notify || ctx.Repo == nil || !ctx.Repo.NotificationsEnabled() {
				saveNotificationResult(ctx, "skipped", "", "notifications disabled")
				return nil
			}
			if !ctx.Update.Changed && ctx.Source != "web_manual" && ctx.Source != "tui_manual" {
				saveNotificationResult(ctx, "skipped", "", "no changes")
				return nil
			}
			prefix := ctx.Config["title_prefix"]
			if prefix == "" {
				prefix = "Pulled"
			}
			title := fmt.Sprintf("%s: %s", prefix, ctx.Repo.Name)
			go func() {
				if err := notifyURL(ctx.AppName, title, ctx.Update.Result, ctx.OpenURL); err != nil {
					saveNotificationResult(ctx, "error", ctx.OpenURL, err.Error())
					ctx.Logger.Error("failed to send pull notification", "repo", ctx.Repo.Name, "err", err.Error())
					return
				}
				saveNotificationResult(ctx, "success", ctx.OpenURL, "")
			}()
			return nil
		},
	}
}

func saveNotificationResult(ctx Context, status, result, errText string) {
	if ctx.Store == nil || ctx.Update.ID == 0 {
		return
	}
	if err := ctx.Store.SavePluginResult(ctx.Update.ID, NotificationsID, status, result, errText); err != nil && ctx.Logger != nil {
		repoName := ""
		if ctx.Repo != nil {
			repoName = ctx.Repo.Name
		}
		ctx.Logger.Error("failed to save notification plugin result", "repo", repoName, "err", err.Error())
	}
}
