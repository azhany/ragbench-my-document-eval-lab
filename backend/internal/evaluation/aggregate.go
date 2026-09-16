package evaluation

import "sort"

// Aggregation semantics (explicit, mirrored in EVALUATION.md):
//   - Latency percentiles are computed over completed results only — cases
//     whose query failed contribute neither latency nor tokens, and
//     evaluator_failed cases still completed their query, so they do
//     contribute latency but their judge scores stay missing.
//   - Undefined (missing) scores are never zero: they are surfaced as
//     missing_score counts with their own denominators.
//   - Failure counts distinguish query_failed from evaluator_failed.

type ResultStatus int

const (
	StatusCompleted ResultStatus = iota
	StatusQueryFailed
	StatusEvaluatorFailed
)

// ResultObservation is one scored case reduced to the aggregate inputs.
type ResultObservation struct {
	Status             ResultStatus
	TotalLatencyMS     int64 // 0 = unknown/missing (query failed or trace lacked it)
	RetrievalLatencyMS int64
	HasRecall          bool
	Recall             float64
	HasMRR             bool
	MRR                float64
	HasRelevance       bool
	AnswerRelevance    int
	HasGroundedness    bool
	Groundedness       int
	HasNDCG            bool
	NDCG               float64
	HasCost            bool
	Cost               float64
}

// RunAggregate is the RB-16 aggregate-card contract: quality, efficiency and
// failure counts stay separate; each metric carries its own denominator.
type RunAggregate struct {
	TotalCases      int `json:"total_cases"`
	Completed       int `json:"completed"`
	QueryFailed     int `json:"query_failed"`
	EvaluatorFailed int `json:"evaluator_failed"`
	// Quality
	RecallCount         int      `json:"recall_count"`
	RecallMean          *float64 `json:"recall_mean"`
	MRRCount            int      `json:"mrr_count"`
	MRRMean             *float64 `json:"mrr_mean"`
	RelevanceCount      int      `json:"answer_relevance_count"`
	AnswerRelevanceMean *float64 `json:"answer_relevance_mean"`
	GroundednessCount   int      `json:"groundedness_count"`
	GroundednessMean    *float64 `json:"groundedness_mean"`
	CitationCount       int      `json:"citation_count"`
	CitationMean        *float64 `json:"citation_correct_mean"`
	NDCGCount           int      `json:"ndcg_count"`
	NDCGMean            *float64 `json:"ndcg_mean"`
	// Efficiency — over completed queries (see doc comment).
	LatencyPopulation int      `json:"latency_population"`
	LatencyP50MS      *int64   `json:"latency_p50_ms"`
	LatencyP95MS      *int64   `json:"latency_p95_ms"`
	CostPopulation    int      `json:"cost_population"`
	CostTotal         *float64 `json:"cost_total"`
	// Missing scores joined with failures for epitaph-free diagnostics.
	MissingScores int `json:"missing_score_cases"`
}

// percentile computes the explicit pXy method: nearest-rank via sorted
// population index ceil(p*(n-1)) over the sorted values (0-based). For
// n >= 1 this always yields a value actually present in the population.
func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func mean(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	total := 0.0
	for _, v := range values {
		total += v
	}
	m := total / float64(len(values))
	return &m
}

// Aggregate folds one run's result observations into the explicit Aggregation
// semantics above.
func Aggregate(observations []ResultObservation) RunAggregate {
	agg := RunAggregate{TotalCases: len(observations)}
	var latencies []int64
	var recalls, mrrs, rel, ground, ndcgs, costs []float64

	for _, o := range observations {
		switch o.Status {
		case StatusCompleted:
			agg.Completed++
		case StatusQueryFailed:
			agg.QueryFailed++
		case StatusEvaluatorFailed:
			agg.EvaluatorFailed++
		}

		if o.Status == StatusCompleted || o.Status == StatusEvaluatorFailed {
			if o.TotalLatencyMS > 0 {
				latencies = append(latencies, o.TotalLatencyMS)
			}
		}
		if o.HasRecall {
			agg.RecallCount++
			recalls = append(recalls, o.Recall)
		}
		if o.HasMRR {
			agg.MRRCount++
			mrrs = append(mrrs, o.MRR)
		}
		if o.HasRelevance {
			agg.RelevanceCount++
			rel = append(rel, float64(o.AnswerRelevance))
		}
		if o.HasGroundedness {
			agg.GroundednessCount++
			ground = append(ground, float64(o.Groundedness))
		}
		if o.HasNDCG {
			agg.NDCGCount++
			ndcgs = append(ndcgs, o.NDCG)
		}
		if o.HasCost {
			agg.CostPopulation++
			costs = append(costs, o.Cost)
		}
		if !o.HasRecall && !o.HasMRR && o.Status != StatusQueryFailed {
			agg.MissingScores++
		}
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	agg.LatencyPopulation = len(latencies)
	if len(latencies) > 0 {
		p50 := percentile(latencies, 0.50)
		p95 := percentile(latencies, 0.95)
		agg.LatencyP50MS, agg.LatencyP95MS = &p50, &p95
	}

	if agg.CostPopulation > 0 {
		total := sum(costs)
		agg.CostTotal = &total
	}
	agg.RecallMean = mean(recalls)
	agg.MRRMean = mean(mrrs)
	agg.AnswerRelevanceMean = mean(rel)
	agg.GroundednessMean = mean(ground)
	agg.NDCGMean = mean(ndcgs)
	return agg
}

func sum(values []float64) float64 {
	total := 0.0
	for _, v := range values {
		total += v
	}
	return total
}
