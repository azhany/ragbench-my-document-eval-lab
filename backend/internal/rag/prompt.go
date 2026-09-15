package rag

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"ragbench-my/backend/internal/providers"
	"ragbench-my/backend/internal/ragconfig"
)

// Explicit context budgeting rules. A chunk is included whole; chunks are
// added in rank order until the budget would be exceeded. chunk_size is
// capped at 8192 characters by rag_configs, so the budget always admits at
// least one chunk and never silently truncates a chunk mid-text.
const (
	// ContextBudgetChars bounds the total evidence characters sent as prompt
	// context (about 6k tokens for gpt-4o-mini).
	ContextBudgetChars = 24000
	// SnippetMaxChars bounds the citation snippet returned in the chat
	// response; the full chunk text stays recoverable from the trace detail
	// context snapshot.
	SnippetMaxChars = 400
)

// SelectContext truncates ranked evidence to the context budget, keeping
// whole chunks in rank order. It returns the included subset.
func SelectContext(evidence []Evidence, budget int) []Evidence {
	selected := make([]Evidence, 0, len(evidence))
	used := 0
	for _, e := range evidence {
		if used+utf8.RuneCountInString(e.Content) > budget {
			break
		}
		used += utf8.RuneCountInString(e.Content)
		selected = append(selected, e)
	}
	return selected
}

// BuildPrompt renders the configured prompt version with the question and
// the budgeted context. The returned text is exactly what is sent to the
// model; the caller persists it in the trace's prompt snapshot so the
// instructions are recoverable later.
func BuildPrompt(cfg ragconfig.Config, question string, context []Evidence) (string, *Error) {
	prompt, err := providers.PromptByVersion(cfg.PromptVersion)
	if err != nil {
		return "", newError(ErrCodeCapabilityUnavailable, "%v", err)
	}

	var evidenceRows strings.Builder
	for i, e := range context {
		fmt.Fprintf(&evidenceRows, "[%d] %s\n", i+1, e.Content)
	}

	text := renderPromptTemplate(prompt.Template, question,
		strings.TrimRight(evidenceRows.String(), "\n"))
	return text, nil
}

// renderPromptTemplate substitutes only markers from the original template.
// It never scans question or evidence replacements, so marker-looking source
// text remains evidence/content instead of becoming another template slot.
func renderPromptTemplate(template, question, evidence string) string {
	const (
		questionMarker = "{{QUESTION}}"
		evidenceMarker = "{{EVIDENCE}}"
	)
	var rendered strings.Builder
	rendered.Grow(len(template) + len(question) + len(evidence))
	for len(template) > 0 {
		questionIndex := strings.Index(template, questionMarker)
		evidenceIndex := strings.Index(template, evidenceMarker)
		index := -1
		replacement := ""
		markerLength := 0
		switch {
		case questionIndex >= 0 && (evidenceIndex < 0 || questionIndex < evidenceIndex):
			index, replacement, markerLength = questionIndex, question, len(questionMarker)
		case evidenceIndex >= 0:
			index, replacement, markerLength = evidenceIndex, evidence, len(evidenceMarker)
		default:
			rendered.WriteString(template)
			return rendered.String()
		}
		rendered.WriteString(template[:index])
		rendered.WriteString(replacement)
		template = template[index+markerLength:]
	}
	return rendered.String()
}

// promptSnapshot is what the trace persists for the prompt_build stage.
type promptSnapshot struct {
	PromptVersion    string `json:"prompt_version"`
	PromptIdentifier string `json:"prompt_identifier"`
	PromptText       string `json:"prompt_text"`
	ContextChunks    int    `json:"context_chunks"`
	ContextChars     int    `json:"context_chars"`
}

func snapshotPrompt(cfg ragconfig.Config, promptText string, context []Evidence) promptSnapshot {
	identifier := ""
	if p, err := providers.PromptByVersion(cfg.PromptVersion); err == nil {
		identifier = p.Identifier
	}
	chars := 0
	for _, e := range context {
		chars += utf8.RuneCountInString(e.Content)
	}
	return promptSnapshot{
		PromptVersion:    cfg.PromptVersion,
		PromptIdentifier: identifier,
		PromptText:       promptText,
		ContextChunks:    len(context),
		ContextChars:     chars,
	}
}
