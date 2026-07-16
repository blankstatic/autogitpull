package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/git"
)

const AISummaryID = "ai_summary"
const defaultOpenAIBaseURL = "https://api.openai.com/v1"
const defaultAISummaryPrompt = "Summarize a git pull for a developer. Be concise. Include notable commits, changed areas, and potential follow-up work. Avoid hype."
const maxAIChangeContextBytes = 120000
const defaultMaxAIFileDiffBytes = 20000
const defaultAIDiffContextLines = 20
const aiMetadataBudgetPercent = 25
const maxAIOmissionDetails = 100

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
			"provider":            "Default",
			"api_type":            "responses",
			"url":                 defaultOpenAIBaseURL,
			"token":               "",
			"model":               "",
			"prompt":              defaultAISummaryPrompt,
			"code_detail":         "limited",
			"include_patterns":    "",
			"exclude_patterns":    ".env*,**/.env*,**/*secret*,**/*credential*,**/*.pem,**/*.key,vendor/**,node_modules/**,dist/**,*.lock,**/*.lock",
			"max_context_bytes":   strconv.Itoa(maxAIChangeContextBytes),
			"max_file_diff_bytes": strconv.Itoa(defaultMaxAIFileDiffBytes),
			"diff_context_lines":  strconv.Itoa(defaultAIDiffContextLines),
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
			{Key: "prompt", Label: "Prompt", Type: "textarea"},
			{Key: "code_detail", Label: "Code sent for analysis", Type: "select", Help: "No code sends only commit and file statistics. Limited sends up to the per-file limit. Full sends complete file diffs while space remains.", Options: []FieldOption{
				{Value: "none", Label: "No code — metadata only"},
				{Value: "limited", Label: "Limited code per file (recommended)"},
				{Value: "full", Label: "Full file diffs"},
			}},
			{Key: "include_patterns", Label: "Only include files", Type: "text", Help: "Optional comma-separated patterns, for example **/*.go,docs/**. Leave empty to consider every changed file."},
			{Key: "exclude_patterns", Label: "Never send these files", Type: "text", Help: "Comma-separated patterns. Matching files are listed, but their code is never sent. Defaults protect common secrets, dependencies, builds, and lock files."},
			{Key: "max_context_bytes", Label: "Maximum request context (bytes)", Type: "number", Help: "Total budget for change metadata and code. Allowed: 256–2000000 bytes. Default: 120000."},
			{Key: "max_file_diff_bytes", Label: "Maximum code per file (bytes)", Type: "number", Help: "Used by Limited mode so one large file cannot consume the request. Allowed: 64 bytes up to the total context limit. Default: 20000."},
			{Key: "diff_context_lines", Label: "Context around changes", Type: "select", Help: "Unchanged lines shown before and after each changed block. Lower values fit more files; higher values give the model more local context.", Options: []FieldOption{
				{Value: "3", Label: "3 lines — compact"},
				{Value: "10", Label: "10 lines"},
				{Value: "20", Label: "20 lines — recommended"},
				{Value: "40", Label: "40 lines"},
				{Value: "80", Label: "80 lines — detailed"},
			}},
		},
		ValidateConfig: validateAISummaryConfig,
		Run: func(ctx Context) error {
			if ctx.Store == nil || ctx.Repo == nil || ctx.Update.ID == 0 {
				return nil
			}
			if ctx.Update.BeforeRev == "" || ctx.Update.AfterRev == "" || ctx.Update.BeforeRev == ctx.Update.AfterRev {
				return ctx.Store.SavePluginResult(ctx.Update.ID, AISummaryID, "skipped", "", "missing revision range")
			}

			context, err := BuildAISummaryChangeContext(ctx.Repo.Path, ctx.Update.BeforeRev, ctx.Update.AfterRev, ctx.Config)
			if err != nil {
				_ = ctx.Store.SavePluginResult(ctx.Update.ID, AISummaryID, "error", "", err.Error())
				return err
			}
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

func validateAISummaryConfig(cfg map[string]string) error {
	mode := strings.TrimSpace(cfg["code_detail"])
	if mode == "" {
		mode = "limited"
	}
	if mode != "none" && mode != "limited" && mode != "full" {
		return fmt.Errorf("code sent for analysis has an invalid value")
	}
	contextValue := strings.TrimSpace(cfg["max_context_bytes"])
	if contextValue == "" {
		contextValue = strconv.Itoa(maxAIChangeContextBytes)
	}
	contextLimit, err := strconv.Atoi(contextValue)
	if err != nil || contextLimit < 256 || contextLimit > 2_000_000 {
		return fmt.Errorf("maximum request context must be between 256 and 2000000 bytes")
	}
	fileValue := strings.TrimSpace(cfg["max_file_diff_bytes"])
	if fileValue == "" {
		fileValue = strconv.Itoa(defaultMaxAIFileDiffBytes)
	}
	fileLimit, err := strconv.Atoi(fileValue)
	if err != nil || fileLimit < 64 || fileLimit > contextLimit {
		return fmt.Errorf("maximum code per file must be between 64 bytes and the total context limit")
	}
	contextLines := strings.TrimSpace(cfg["diff_context_lines"])
	if contextLines == "" {
		contextLines = strconv.Itoa(defaultAIDiffContextLines)
	}
	if contextLines != "3" && contextLines != "10" && contextLines != "20" && contextLines != "40" && contextLines != "80" {
		return fmt.Errorf("context around changes has an invalid value")
	}
	return nil
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

func AISummaryPrompt(cfg map[string]string) string {
	if prompt := strings.TrimSpace(cfg["prompt"]); prompt != "" {
		return prompt
	}
	return defaultAISummaryPrompt
}

func AISummaryInput(repoName, context string) string {
	return fmt.Sprintf("Repository: %s\n\nChange context:\n%s", repoName, context)
}

func BuildAISummaryChangeContext(repoPath, beforeRev, afterRev string, configs ...map[string]string) (string, error) {
	logText, err := git.GitChangedLog(repoPath, beforeRev, afterRev)
	if err != nil {
		return "", err
	}
	diffStat, err := git.GitDiffStat(repoPath, beforeRev, afterRev)
	if err != nil {
		return "", err
	}
	files, err := git.GitChangedFiles(repoPath, beforeRev, afterRev)
	if err != nil {
		return "", err
	}
	cfg := map[string]string{}
	if len(configs) > 0 && configs[0] != nil {
		cfg = configs[0]
	}
	contextLimit := boundedPositiveInt(cfg["max_context_bytes"], maxAIChangeContextBytes, 256, 2_000_000)
	fileLimit := boundedPositiveInt(cfg["max_file_diff_bytes"], defaultMaxAIFileDiffBytes, 64, contextLimit)
	patchLimit := contextLimit
	contextLines := boundedPositiveInt(cfg["diff_context_lines"], defaultAIDiffContextLines, 0, 200)
	if strings.TrimSpace(cfg["code_detail"]) != "full" && fileLimit < patchLimit {
		patchLimit = fileLimit
	}
	return buildAISummaryChangeContextWithConfig(logText, diffStat, files, func(filePath string) (string, error) {
		patch, truncated, err := git.GitDiffPatchForFileLimitedContext(repoPath, beforeRev, afterRev, filePath, patchLimit, contextLines)
		if truncated {
			patch += "\n[git diff output capped before context assembly]"
		}
		return patch, err
	}, cfg)
}

func buildAISummaryChangeContext(logText, diffStat string, files []string, diffForFile func(string) (string, error)) (string, error) {
	return buildAISummaryChangeContextWithConfig(logText, diffStat, files, diffForFile, map[string]string{"code_detail": "full"})
}

func buildAISummaryChangeContextWithConfig(logText, diffStat string, files []string, diffForFile func(string) (string, error), cfg map[string]string) (string, error) {
	contextLimit := boundedPositiveInt(cfg["max_context_bytes"], maxAIChangeContextBytes, 256, 2_000_000)
	fileLimit := boundedPositiveInt(cfg["max_file_diff_bytes"], defaultMaxAIFileDiffBytes, 64, contextLimit)
	codeDetail := strings.TrimSpace(cfg["code_detail"])
	if codeDetail != "none" && codeDetail != "limited" && codeDetail != "full" {
		codeDetail = "limited"
	}
	sections := []string{
		sectionText("Commits", logText),
		sectionText("Diff summary", diffStat),
	}
	base := strings.TrimSpace(strings.Join(nonEmpty(sections), "\n\n"))
	if base == "" && len(files) == 0 {
		return "", nil
	}
	metadataLimit := contextLimit
	if codeDetail != "none" {
		metadataLimit = contextLimit * aiMetadataBudgetPercent / 100
		if metadataLimit < 1 {
			metadataLimit = 1
		}
	}
	if len(base) > metadataLimit {
		base = truncateAIChangeContextTo(base, metadataLimit)
	}

	var included []string
	var omitted []string
	omittedCount := 0
	addOmitted := func(detail string) {
		omittedCount++
		if len(omitted) < maxAIOmissionDetails {
			omitted = append(omitted, detail)
		}
	}
	var diffSections []string
	used := len(base)
	codeHeaderCost := len("\n\nSelected unified code diffs:\n")
	omissionReserve := contextLimit / 10
	if omissionReserve > 4096 {
		omissionReserve = 4096
	}
	for index, file := range files {
		if codeDetail == "none" {
			addOmitted(file + " — code sending disabled")
			continue
		}
		if !matchesInclude(file, cfg["include_patterns"]) {
			addOmitted(file + " — not matched by include patterns")
			continue
		}
		if matchesAnyPattern(file, cfg["exclude_patterns"]) {
			addOmitted(file + " — matched exclude patterns")
			continue
		}
		remaining := contextLimit - used - codeHeaderCost - omissionReserve
		if remaining <= len("File diff: ")+len(file)+64 {
			addOmitted(file + " — total context budget exhausted")
			for _, rest := range files[index+1:] {
				addOmitted(rest + " — not inspected because context budget was exhausted")
			}
			break
		}
		diffText, err := diffForFile(file)
		if err != nil {
			return "", err
		}
		diffText = strings.TrimSpace(diffText)
		if diffText == "" {
			continue
		}
		effectiveFileLimit := remaining - len("File diff: ") - len(file) - 3
		truncationMarker := "\n[truncated: file diff exceeded available budget]"
		if codeDetail == "limited" && fileLimit < effectiveFileLimit {
			effectiveFileLimit = fileLimit
			truncationMarker = "\n[truncated: file diff exceeded per-file limit]"
		}
		if len(diffText) > effectiveFileLimit {
			diffText = truncateTextWithMarker(diffText, effectiveFileLimit, truncationMarker)
		}
		section := fmt.Sprintf("File diff: %s\n%s", file, diffText)
		cost := len(section) + 2
		if used+cost+codeHeaderCost+omissionReserve > contextLimit {
			addOmitted(file + " — total context budget exhausted")
			continue
		}
		included = append(included, file)
		diffSections = append(diffSections, section)
		used += cost
	}

	var out []string
	if base != "" {
		out = append(out, base)
	}
	if len(diffSections) > 0 {
		out = append(out, "Selected unified code diffs:\n"+strings.Join(diffSections, "\n\n"))
	}
	if omittedCount > 0 {
		details := omitted
		if omittedCount > len(details) {
			details = append(append([]string{}, details...), fmt.Sprintf("... and %d more omitted files", omittedCount-len(details)))
		}
		out = append(out, fmt.Sprintf("Files without code diffs (%d included, %d omitted):\n%s", len(included), omittedCount, strings.Join(details, "\n")))
	}
	return truncateAIChangeContextTo(strings.TrimSpace(strings.Join(out, "\n\n")), contextLimit), nil
}

func sectionText(title, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	return title + ":\n" + body
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func positiveInt(value string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func boundedPositiveInt(value string, fallback, minimum, maximum int) int {
	n := positiveInt(value, fallback)
	if n < minimum {
		return minimum
	}
	if n > maximum {
		return maximum
	}
	return n
}

func matchesInclude(file, patterns string) bool {
	return strings.TrimSpace(patterns) == "" || matchesAnyPattern(file, patterns)
}

func matchesAnyPattern(file, patterns string) bool {
	file = strings.TrimPrefix(strings.ReplaceAll(file, "\\", "/"), "./")
	for _, pattern := range strings.FieldsFunc(patterns, func(r rune) bool { return r == ',' || r == '\n' }) {
		pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
		if pattern == "" {
			continue
		}
		matched, err := regexp.MatchString(globRegexp(pattern), file)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func globRegexp(pattern string) string {
	runes := []rune(pattern)
	var out strings.Builder
	out.WriteString("^")
	for i := 0; i < len(runes); {
		switch runes[i] {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				i += 2
				if i < len(runes) && runes[i] == '/' {
					out.WriteString("(?:.*/)?")
					i++
				} else {
					out.WriteString(".*")
				}
			} else {
				out.WriteString("[^/]*")
				i++
			}
		case '?':
			out.WriteString("[^/]")
			i++
		default:
			out.WriteString(regexp.QuoteMeta(string(runes[i])))
			i++
		}
	}
	out.WriteString("$")
	return out.String()
}

func truncateAIChangeContext(context string) string {
	return truncateAIChangeContextTo(context, maxAIChangeContextBytes)
}

func truncateAIChangeContextTo(context string, limit int) string {
	if len(context) <= limit {
		return context
	}
	marker := fmt.Sprintf("\n\n[truncated: change context exceeded %d bytes]", limit)
	if len(marker) >= limit {
		return marker[:limit]
	}
	end := limit - len(marker)
	for end > 0 && !utf8.RuneStart(context[end]) {
		end--
	}
	return context[:end] + marker
}

func truncateTextWithMarker(text string, limit int, marker string) string {
	if len(text) <= limit {
		return text
	}
	if len(marker) >= limit {
		return marker[:limit]
	}
	end := limit - len(marker)
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + marker
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
		"instructions": AISummaryPrompt(cfg),
		"input":        AISummaryInput(repoName, context),
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
			{"role": "system", "content": AISummaryPrompt(cfg)},
			{"role": "user", "content": AISummaryInput(repoName, context)},
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
