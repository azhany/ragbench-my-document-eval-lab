package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ragbench-my/backend/internal/providers"
)

// Evaluator policy bounds (mirrored in migration documentation; keep in sync).
const (
	MinScoringK   = 1
	MaxScoringK   = 100
	PolicyVersion = "evaluator-policy-v1"
)

// EvaluatorPolicyVersioned is the pinned evaluation policy stored per run:
// rubric versions and scoring K, persisted before scoring, from RB-13/RB-15.
type EvaluatorPolicyVersioned struct {
	PolicyVersion string `json:"policy_version"`
	// RubricVersion is the versioned judge rubric (model + template identity
	// in providers). Unknown versions fail validation instead of scoring with
	// something newer.
	RubricVersion string `json:"rubric_version"`
	// ScoringK is the K used for Recall@K evaluation.
	ScoringK int `json:"scoring_k"`
	// NDCGPolicyVersion pins gain/discount/zero-ideal semantics even when a
	// dataset has no graded judgments. It prevents silent policy mixing.
	NDCGPolicyVersion string `json:"ndcg_policy_version"`
}

// ErrPolicyNotFound marks a request for an unknown rubric version.
var ErrPolicyNotFound = errors.New("unknown rubric version")

func ResolveEvaluatorPolicy(rubricVersion string, scoringK int) (EvaluatorPolicyVersioned, error) {
	if strings.TrimSpace(rubricVersion) == "" {
		return EvaluatorPolicyVersioned{}, fmt.Errorf("rubric_version is required")
	}
	if _, err := providers.RubricByVersion(strings.TrimSpace(rubricVersion)); err != nil {
		return EvaluatorPolicyVersioned{}, ErrPolicyNotFound
	}
	if scoringK < MinScoringK || scoringK > MaxScoringK {
		return EvaluatorPolicyVersioned{}, fmt.Errorf("scoring_k must be between %d and %d", MinScoringK, MaxScoringK)
	}
	return EvaluatorPolicyVersioned{PolicyVersion: PolicyVersion, RubricVersion: strings.TrimSpace(rubricVersion), ScoringK: scoringK, NDCGPolicyVersion: NDCGPolicyVersion}, nil
}

// Citation scoring — citation correctness.
//
// A citation is "correct" when the cited evidence chunk actually appeared in
// the ranked retrieval for that request (i.e. the answer's citation points
// at evidence the pipeline really used). citation_correct = correct / total.
// No citations at all leaves the value nil (not evaluable, not zero).

// ScoredCitation is one parsed citation with its document identity.
type ScoredCitation struct {
	Number     int
	DocumentID string
}

// ScoreCitations computes the fraction of citations whose cited chunk
// document belongs to the retrieved evidence set actually sent.
// No citations ⇒ (0, false): undefined, never zero.
func ScoreCitations(citations []ScoredCitation, retrievedDocs map[string]bool) (float64, bool) {
	if len(citations) == 0 {
		return 0, false
	}
	correct := 0
	for _, c := range citations {
		if retrievedDocs[c.DocumentID] {
			correct++
		}
	}
	return float64(correct) / float64(len(citations)), true
}

// Judge rubric (versioned in the provider registry).

// JudgeInput is everything one judge call sees.
type JudgeInput struct {
	Question        string
	ReferenceAnswer string
	Answer          string
	CitedEvidence   string // numbered evidence text sent to the model
}

// JudgeScore is one 1–5 score with a verbatim rationale.
type JudgeScore struct {
	Score     int    `json:"score"`
	Rationale string `json:"rationale"`
}

// JudgeScores holds one rubric's parsed judgment plus usage for evaluator
// cost accounting — evaluator cost is tracked separately from query cost
// (explicit rates; unreported usage stays null, never zero).
type JudgeScores struct {
	AnswerRelevance *JudgeScore
	Groundedness    *JudgeScore
	InputTokens     *int
	OutputTokens    *int
}

var judgeParseError = errors.New("judge output is not a valid rubric judgment")

// renderRubric substitutes the rubric template's markers. Untrusted case
// content is data, never instructions; the template instructs the judge to
// treat it as evidence/context only.
func renderRubric(r providers.Rubric, in JudgeInput) string {
	replacer := strings.NewReplacer(
		"{{QUESTION}}", in.Question,
		"{{REFERENCE_ANSWER}}", in.ReferenceAnswer,
		"{{ANSWER}}", in.Answer,
		"{{CITED_EVIDENCE}}", in.CitedEvidence,
	)
	return replacer.Replace(r.Template)
}

// RunJudge executes the versioned rubric judge for one case using any
// generation provider. A provider failure, non-JSON output, out-of-range
// score, or missing field is an evaluator failure — never a zero score.
func RunJudge(ctx context.Context, generator providers.Generator, rubricVersion string, in JudgeInput) (JudgeScores, error) {
	rubric, err := providers.RubricByVersion(rubricVersion)
	if err != nil {
		return JudgeScores{}, err
	}
	res, err := generator.Generate(ctx, providers.GenerationProfile{
		Provider: rubric.JudgeProvider,
		Model:    rubric.JudgeModel,
	}, renderRubric(rubric, in))
	if err != nil {
		return JudgeScores{}, fmt.Errorf("judge model call failed: %w", err)
	}
	scores, err := parseJudgeOutput(res.Text)
	if err != nil {
		return JudgeScores{}, err
	}
	scores.InputTokens = res.InputTokens
	scores.OutputTokens = res.OutputTokens
	return scores, nil
}

// parseJudgeOutput recognizes the rubric's documented output shape:
// {"answer_relevance": {"score": n, "rationale": "..."},
//
//	"groundedness":    {"score": n, "rationale": "..."}}
func parseJudgeOutput(text string) (JudgeScores, error) {
	cut := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if cut < 0 || end <= cut {
		return JudgeScores{}, judgeParseError
	}
	var parsed struct {
		AnswerRelevance *JudgeScore `json:"answer_relevance"`
		Groundedness    *JudgeScore `json:"groundedness"`
	}
	if err := json.Unmarshal([]byte(text[cut:end+1]), &parsed); err != nil {
		return JudgeScores{}, judgeParseError
	}
	if parsed.AnswerRelevance == nil || parsed.Groundedness == nil {
		return JudgeScores{}, judgeParseError
	}
	for _, s := range []*JudgeScore{parsed.AnswerRelevance, parsed.Groundedness} {
		if s.Score < 1 || s.Score > 5 {
			return JudgeScores{}, judgeParseError
		}
	}
	return JudgeScores{AnswerRelevance: parsed.AnswerRelevance, Groundedness: parsed.Groundedness}, nil
}
