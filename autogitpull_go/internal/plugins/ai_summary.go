package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/git"
)

const AISummaryID = "ai_summary"
const defaultOpenAIBaseURL = "https://api.openai.com/v1"
const aiSummaryPrompt = "Summarize a git pull for a developer. Be concise. Include notable commits, changed areas, and potential follow-up work. Avoid hype."

var errAIProviderNotConfigured = errors.New("AI provider is not configured")

var aiSummaryHTTPClient = &http.Client{Timeout: 45 * time.Second}

func AISummaryHTTPClientForTest() *http.Client {
	return aiSummaryHTTPClient
}

func SetAISummaryHTTPClientForTest(client *http.Client) {
	aiSummaryHTTPClient = client
}

func aiSummaryPlugin() Definition {
	return Definition{
		ID:          AISummaryID,
		Name:        "AI summary",
		Description: "Prepare AI-ready summaries for repository updates.",
		DefaultOn:   false,
		DefaultConfig: map[string]string{
			"provider": "Default",
			"api_type": "responses",
			"url":      defaultOpenAIBaseURL,
			"token":    "",
			"model":    "",
		},
		Fields: []Field{
			{Key: "provider", Label: "Provider name", Type: "text"},
			{Key: "api_type", Label: "API type", Type: "select", Options: []FieldOption{
				{Value: "responses", Label: "Responses"},
				{Value: "chat_completions", Label: "Chat completions"},
			}},
			{Key: "url", Label: "API URL", Type: "url"},
			{Key: "token", Label: "API key", Type: "password"},
			{Key: "model", Label: "Model", Type: "text"},
		},
		Run: func(ctx Context) error {
			if ctx.Store == nil || ctx.Repo == nil || ctx.Update.ID == 0 {
				return nil
			}
			if ctx.Update.BeforeRev == "" || ctx.Update.AfterRev == "" || ctx.Update.BeforeRev == ctx.Update.AfterRev {
				return ctx.Store.SavePluginResult(ctx.Update.ID, AISummaryID, "skipped", "", "missing revision range")
			}

			logText, err := git.GitChangedLog(ctx.Repo.Path, ctx.Update.BeforeRev, ctx.Update.AfterRev)
			if err != nil {
				_ = ctx.Store.SavePluginResult(ctx.Update.ID, AISummaryID, "error", "", err.Error())
				return err
			}
			diffStat, err := git.GitDiffStat(ctx.Repo.Path, ctx.Update.BeforeRev, ctx.Update.AfterRev)
			if err != nil {
				_ = ctx.Store.SavePluginResult(ctx.Update.ID, AISummaryID, "error", "", err.Error())
				return err
			}

			context := strings.TrimSpace(logText + "\n\n" + diffStat)
			if context == "" {
				return ctx.Store.SavePluginResult(ctx.Update.ID, AISummaryID, "skipped", "", "empty change context")
			}

			summary, err := generateAISummary(ctx.Config, ctx.Repo.Name, context)
			if err != nil {
				if errors.Is(err, errAIProviderNotConfigured) {
					return ctx.Store.SavePluginResult(ctx.Update.ID, AISummaryID, "skipped", "", err.Error())
				}
				_ = ctx.Store.SavePluginResult(ctx.Update.ID, AISummaryID, "error", context, err.Error())
				return err
			}
			if summary == "" {
				return ctx.Store.SavePluginResult(ctx.Update.ID, AISummaryID, "error", context, "AI provider returned empty response")
			}
			return ctx.Store.SavePluginResult(ctx.Update.ID, AISummaryID, "success", summary, "")
		},
	}
}

func generateAISummary(cfg map[string]string, repoName, context string) (string, error) {
	if strings.TrimSpace(cfg["url"]) == "" || strings.TrimSpace(cfg["token"]) == "" || strings.TrimSpace(cfg["model"]) == "" {
		return "", errAIProviderNotConfigured
	}
	return callAIProvider(cfg, repoName, context)
}

func TestAISummary(cfg map[string]string) (string, error) {
	return generateAISummary(cfg, "test", "hello")
}

func callAIProvider(cfg map[string]string, repoName, context string) (string, error) {
	switch cfg["api_type"] {
	case "", "responses":
		return callOpenAIResponses(cfg, repoName, context)
	case "chat_completions":
		return callChatCompletions(cfg, repoName, context)
	default:
		return "", fmt.Errorf("unsupported AI API type: %s", cfg["api_type"])
	}
}

func callOpenAIResponses(cfg map[string]string, repoName, context string) (string, error) {
	token := strings.TrimSpace(cfg["token"])
	if token == "" {
		return "", fmt.Errorf("missing API key")
	}
	model := strings.TrimSpace(cfg["model"])
	if model == "" {
		return "", fmt.Errorf("missing model")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg["url"]), "/")
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}

	body := map[string]any{
		"model":        model,
		"instructions": aiSummaryPrompt,
		"input":        fmt.Sprintf("Repository: %s\n\nGit log/stat context:\n%s", repoName, context),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := aiSummaryHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("AI provider returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	return responseOutputText(respBody), nil
}

func callChatCompletions(cfg map[string]string, repoName, context string) (string, error) {
	token := strings.TrimSpace(cfg["token"])
	if token == "" {
		return "", fmt.Errorf("missing API key")
	}
	model := strings.TrimSpace(cfg["model"])
	if model == "" {
		return "", fmt.Errorf("missing model")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg["url"]), "/")
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}

	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": aiSummaryPrompt},
			{"role": "user", "content": fmt.Sprintf("Repository: %s\n\nGit log/stat context:\n%s", repoName, context)},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := aiSummaryHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("AI provider returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	return chatCompletionText(respBody), nil
}

func responseOutputText(data []byte) string {
	var parsed struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ""
	}
	if strings.TrimSpace(parsed.OutputText) != "" {
		return strings.TrimSpace(parsed.OutputText)
	}
	var parts []string
	for _, output := range parsed.Output {
		if output.Type != "message" {
			continue
		}
		for _, content := range output.Content {
			if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
				parts = append(parts, strings.TrimSpace(content.Text))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func chatCompletionText(data []byte) string {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ""
	}
	if len(parsed.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content)
}
