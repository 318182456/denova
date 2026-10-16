package agentchat

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"denova/internal/agents/session"
	"denova/internal/i18n"
)

const historyContentPageSize = 100

// Search the durable display transcript, including messages outside the resident
// UI window. Metadata matches need no body scan; the first body hit supplies one
// bounded excerpt, with whitespace collapsed only for its single-line display.
func searchHistoryContent(ctx context.Context, sess *session.Session, query string) (string, error) {
	for before := -1; ; {
		page, err := sess.ReadHistoryPage(ctx, before, historyContentPageSize)
		if err != nil {
			return "", err
		}
		for index := len(page.Entries) - 1; index >= 0; index-- {
			entry := page.Entries[index]
			if !isTranscriptMessage(entry) {
				continue
			}
			text := strings.Join(strings.Fields(entry.Content), " ")
			lower := strings.ToLower(text)
			if match := strings.Index(lower, query); match >= 0 {
				runes := []rune(text)
				position := utf8.RuneCountInString(lower[:match])
				start := max(0, position-8)
				end := min(len(runes), position+utf8.RuneCountInString(query)+80)
				excerpt := string(runes[start:end])
				if start > 0 {
					excerpt = "…" + excerpt
				}
				if end < len(runes) {
					excerpt += "…"
				}
				return excerpt, nil
			}
		}
		if !page.HasMore {
			return "", nil
		}
		before = page.NextBefore
	}
}

func isTranscriptMessage(entry session.HistoryEntry) bool {
	return entry.Type == "message" && !entry.SubAgent && entry.Status != "discarded" &&
		(entry.Content != "" || len(entry.Attachments) > 0) &&
		(entry.Role == "user" || entry.Role == "assistant")
}

// ExportMarkdown exports the saved creator-facing conversation without tools,
// reasoning or model-only context. Reading backwards from the first page fixes
// the export's tail even if a running Agent appends more messages. Page strings
// are joined in chronological order; attachment bytes are never embedded.
func (service *Service) ExportMarkdown(ctx context.Context, binding Binding, locale string) ([]byte, error) {
	binding, err := service.ResolveBinding(binding)
	if err != nil {
		return nil, err
	}
	_, layout, err := service.registry.Resolve(binding.ProjectID, true)
	if err != nil {
		return nil, err
	}
	store, err := service.projectSessionStore(binding.ProjectID, layout.SessionsDir(), false)
	if err != nil {
		return nil, err
	}
	sess, err := store.Get(binding.SessionID)
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	localizer := i18n.New(locale)
	title := strings.Join(strings.Fields(sess.Title()), " ")
	if title == "" {
		title = localizer.T("api.agentChat.untitled")
	}
	pages := make([]string, 0)
	for before := -1; ; {
		page, err := sess.ReadHistoryPage(ctx, before, historyContentPageSize)
		if err != nil {
			return nil, err
		}
		var block strings.Builder
		for _, entry := range page.Entries {
			if !isTranscriptMessage(entry) {
				continue
			}
			role := localizer.T("api.agentChat.exportAssistant")
			if entry.Role == "user" {
				role = localizer.T("api.agentChat.exportUser")
			}
			fmt.Fprintf(&block, "## %s\n\n%s\n\n", role, entry.Content)
			for _, attachment := range entry.Attachments {
				name := strings.Join(strings.Fields(attachment.Name), " ")
				fmt.Fprintf(&block, "%s\n\n", localizer.T("api.agentChat.exportAttachment", "name", name))
			}
		}
		pages = append(pages, block.String())
		if !page.HasMore {
			break
		}
		before = page.NextBefore
	}
	var output strings.Builder
	fmt.Fprintf(&output, "# %s\n\n", title)
	for index := len(pages) - 1; index >= 0; index-- {
		output.WriteString(pages[index])
	}
	slog.InfoContext(ctx, "Exported conversation Markdown", "project_id", binding.ProjectID,
		"session_id", binding.SessionID, "bytes", output.Len(), "duration", time.Since(startedAt))
	return []byte(output.String()), nil
}
