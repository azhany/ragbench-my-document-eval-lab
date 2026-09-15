package rag

import (
	"fmt"
	"strings"
	"testing"
)

func evidenceWithContent(n int) []Evidence {
	context := make([]Evidence, n)
	for i := range context {
		context[i] = Evidence{
			ChunkID:    fmt.Sprintf("chunk-%02d", i+1),
			DocumentID: "doc-1",
			RevisionID: "rev-1",
			ChunkIndex: i,
			Content:    fmt.Sprintf("evidence passage %d", i+1),
			Rank:       i + 1,
		}
	}
	return context
}

func TestMapCitationsHappyPath(t *testing.T) {
	context := evidenceWithContent(3)
	citations, insufficient, err := mapCitations("Approved items need two signatures [1]. Storage is audited [2].", context)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if insufficient {
		t.Fatal("expected non-insufficient answer")
	}
	if len(citations) != 2 {
		t.Fatalf("citations = %d, want 2", len(citations))
	}
	if citations[0].ChunkID != "chunk-01" || citations[0].DocumentID != "doc-1" {
		t.Fatalf("first citation = %+v, want chunk-01", citations[0])
	}
	if citations[1].ChunkID != "chunk-02" {
		t.Fatalf("second citation = %+v, want chunk-02", citations[1])
	}
}

func TestMapCitationsDeduplicatesAndKeepsFirstOrder(t *testing.T) {
	context := evidenceWithContent(3)
	citations, _, err := mapCitations("Two signatures [2] are required [2] and audited [1].", context)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(citations) != 2 {
		t.Fatalf("citations = %d, want 2 (deduplicated)", len(citations))
	}
	if citations[0].ChunkID != "chunk-02" || citations[1].ChunkID != "chunk-01" {
		t.Fatalf("citation order = %v then %v, want chunk-02 then chunk-01", citations[0].ChunkID, citations[1].ChunkID)
	}
}

func TestMapCitationsOutsideContextIsInvalid(t *testing.T) {
	context := evidenceWithContent(3)
	_, _, err := mapCitations("Answer text [4].", context)
	if err == nil || err.Code != ErrCodeCitationInvalid {
		t.Fatalf("want citation_invalid error, got %v", err)
	}
	if !strings.Contains(err.Message, "outside") {
		t.Fatalf("message should name the boundary, got %q", err.Message)
	}
}

func TestMapCitationsZeroIsInvalid(t *testing.T) {
	context := evidenceWithContent(3)
	_, _, err := mapCitations("Answer text [0].", context)
	if err == nil || err.Code != ErrCodeCitationInvalid {
		t.Fatalf("want citation_invalid for [0], got %v", err)
	}
}

func TestMapCitationsMissing(t *testing.T) {
	context := evidenceWithContent(3)
	_, _, err := mapCitations("An uncited answer.", context)
	if err == nil || err.Code != ErrCodeCitationMissing {
		t.Fatalf("want citation_missing, got %v", err)
	}
}

func TestMapCitationsInsufficientMarker(t *testing.T) {
	context := evidenceWithContent(3)
	citations, insufficient, err := mapCitations("INSUFFICIENT_EVIDENCE", context)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !insufficient {
		t.Fatal("expected insufficient outcome")
	}
	if citations != nil {
		t.Fatalf("citations = %v, want nil", citations)
	}
}

func TestMapCitationsInsufficientMarkerWithWhitespace(t *testing.T) {
	context := evidenceWithContent(3)
	_, insufficient, err := mapCitations("  INSUFFICIENT_EVIDENCE\n", context)
	if err != nil || !insufficient {
		t.Fatalf("want insufficient outcome, got insufficient=%v err=%v", insufficient, err)
	}
}

func TestSnippetBounded(t *testing.T) {
	long := strings.Repeat("x", SnippetMaxChars*3)
	got := snippet(long)
	if got == "" {
		t.Fatal("snippet empty")
	}
	if len([]rune(got)) != SnippetMaxChars+1 {
		t.Fatalf("snippet length = %d, want %d (content + ellipsis)", len([]rune(got)), SnippetMaxChars+1)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatal("bounded snippet must end with ellipsis")
	}
	short := snippet("short evidence")
	if short != "short evidence" {
		t.Fatalf("short snippet changed: %q", short)
	}
}
