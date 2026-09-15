package rag

import (
	"strings"
	"testing"
)

func TestBuildPromptDoesNotReinterpretQuestionMarkers(t *testing.T) {
	question := "Can the literal {{EVIDENCE}} and {{QUESTION}} text remain?"
	prompt, err := BuildPrompt(testConfig, question, []Evidence{{Content: "approval evidence"}})
	if err != nil {
		t.Fatalf("unexpected prompt error: %v", err)
	}
	if !strings.Contains(prompt, question) {
		t.Fatalf("prompt lost marker-looking question text: %q", prompt)
	}
	if strings.Count(prompt, "approval evidence") != 1 {
		t.Fatalf("evidence was reinterpreted or duplicated: %q", prompt)
	}
}

func TestSelectContextCountsUnicodeCharacters(t *testing.T) {
	evidence := []Evidence{{Content: "éé"}, {Content: "ok"}}
	selected := SelectContext(evidence, 2)
	if len(selected) != 1 || selected[0].Content != "éé" {
		t.Fatalf("selected = %+v, want one two-rune chunk", selected)
	}
}
