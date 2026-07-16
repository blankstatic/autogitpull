package plugins

import (
	"fmt"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/db"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/notifications"
)

const NotificationsID = "notifications"

var notifyURL = notifications.OSNotifyURL

// TestNotifications sends a standalone notification using the supplied, possibly unsaved, settings.
func TestNotifications(appName string, cfg map[string]string) error {
	prefix := cfg["title_prefix"]
	if prefix == "" {
		prefix = "Pulled"
	}
	return notifyURL(appName, fmt.Sprintf("%s: Test", prefix), "Notifications are working.", "")
}

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
			if !ctx.Notify {
				saveNotificationResult(ctx, "skipped", "", "notification dispatch disabled")
				return nil
			}
			if ctx.Repo == nil {
				saveNotificationResult(ctx, "skipped", "", "missing repo")
				return nil
			}
			if !ctx.Repo.NotificationsEnabled() {
				saveNotificationResult(ctx, "skipped", "", "repo notifications muted")
				return nil
			}
			remoteUpdated := db.IsRemoteUpdatedPullResult(ctx.Update.Result)
			if !ctx.Update.Changed && !remoteUpdated && ctx.Source != "web_manual" && ctx.Source != "tui_manual" {
				saveNotificationResult(ctx, "skipped", "", "no changes")
				return nil
			}
			prefix := ctx.Config["title_prefix"]
			if prefix == "" {
				prefix = "Pulled"
			}
			title := fmt.Sprintf("%s: %s", prefix, ctx.Repo.Name)
			if err := notifyURL(ctx.AppName, title, ctx.Update.Result, ctx.OpenURL); err != nil {
				saveNotificationResult(ctx, "error", ctx.OpenURL, err.Error())
				ctx.Logger.Error("failed to send pull notification", "repo", ctx.Repo.Name, "err", err.Error())
				return nil
			}
			saveNotificationResult(ctx, "success", ctx.OpenURL, "")
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
