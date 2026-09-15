package rag

import (
	"regexp"
	"strconv"
	"strings"
)

// citationMarker matches a numbered citation like [3]. Any signed integer is
// parsed so [0], negative IDs, and large IDs become citation_invalid rather
// than being mistaken for an answer with no citations.
var citationMarker = regexp.MustCompile(`\[(-?[0-9]+)\]`)

// insufficientEvidenceMarker is the documented exact reply for questions the
// evidence cannot answer. It is the prompt's legitimate fallback, not a
// citation failure.
const insufficientEvidenceMarker = "INSUFFICIENT_EVIDENCE"

// Citation maps one cited evidence number to its chunk identity. Snippet is
// bounded by SnippetMaxChars; the full content lives in the trace context
// snapshot.
type Citation struct {
	DocumentID string `json:"document_id"`
	ChunkID    string `json:"chunk_id"`
	Snippet    string `json:"snippet"`
}

// mapCitations parses the model's answer and maps cited evidence numbers to
// the context chunks. Rules:
//   - an exact INSUFFICIENT_EVIDENCE reply is the legitimate insufficient
//     outcome (no citations required, no failure);
//   - a citation number outside 1..len(context) is a visible
//     citation_invalid failure (the model referenced evidence it was not
//     given) — never silently dropped;
//   - any other answer with zero citations is a visible citation_missing
//     failure — no fabricated fallback answer is produced.
func mapCitations(answer string, context []Evidence) ([]Citation, bool, *Error) {
	if strings.TrimSpace(answer) == insufficientEvidenceMarker {
		return nil, true, nil
	}

	positions := map[int]int{}
	order := []int{}
	for _, match := range citationMarker.FindAllStringSubmatch(answer, -1) {
		n, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, false, newError(ErrCodeCitationInvalid, "unparseable citation marker in answer")
		}
		if n < 1 || n > len(context) {
			return nil, false, newError(ErrCodeCitationInvalid,
				"cited evidence index %d is outside the %d retrieved chunks sent as context", n, len(context))
		}
		if _, seen := positions[n]; !seen {
			positions[n] = len(order)
			order = append(order, n)
		}
	}

	if len(order) == 0 {
		return nil, false, newError(ErrCodeCitationMissing,
			"the model answered without citing any retrieved evidence; refusing to return an uncited answer")
	}

	citations := make([]Citation, 0, len(order))
	for _, n := range order {
		e := context[n-1]
		citations = append(citations, Citation{
			DocumentID: e.DocumentID,
			ChunkID:    e.ChunkID,
			Snippet:    snippet(e.Content),
		})
	}
	return citations, false, nil
}

func snippet(content string) string {
	runes := []rune(content)
	if len(runes) <= SnippetMaxChars {
		return content
	}
	return string(runes[:SnippetMaxChars]) + "…"
}
