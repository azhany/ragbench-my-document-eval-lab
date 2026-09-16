// Package metrics provides the database-backed monitoring summary used by
// Overview and Monitor. It deliberately aggregates source records directly;
// no dashboard-specific counters or fabricated zero values are persisted.
package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const DefaultWindow = 30 * 24 * time.Hour

type Filter struct {
	From     time.Time
	To       time.Time
	Timezone string
	Traffic  string // all, query, evaluation
}

type Summary struct {
	Window          Window           `json:"window"`
	Reliability     Reliability      `json:"reliability"`
	Performance     Performance      `json:"performance"`
	Cost            Cost             `json:"cost"`
	QualityTrends   []QualityTrend   `json:"quality_trends"`
	DailyExperiment []DailyCost      `json:"daily_experiment_cost"`
	Failures        []FailureSummary `json:"failures"`
	RegressionCount int              `json:"regression_count"`
}

type Window struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Timezone string    `json:"timezone"`
	Traffic  string    `json:"traffic"`
}

type Reliability struct {
	QueryTotal              int      `json:"query_total"`
	QuerySuccess            int      `json:"query_success"`
	QueryErrors             int      `json:"query_errors"`
	QueryErrorRate          *float64 `json:"query_error_rate"`
	IngestionSucceeded      int      `json:"ingestion_succeeded"`
	IngestionFailed         int      `json:"ingestion_failed"`
	IngestionDispatchFailed int      `json:"ingestion_dispatch_failed"`
	EvaluationTotal         int      `json:"evaluation_total"`
	EvaluationFailed        int      `json:"evaluation_failed"`
	ProviderErrors          int      `json:"provider_errors"`
}

type Performance struct {
	TotalLatencyPopulation int    `json:"total_latency_population"`
	TotalLatencyP50MS      *int64 `json:"total_latency_p50_ms"`
	TotalLatencyP95MS      *int64 `json:"total_latency_p95_ms"`
	RetrievalPopulation    int    `json:"retrieval_population"`
	RetrievalP50MS         *int64 `json:"retrieval_p50_ms"`
	RetrievalP95MS         *int64 `json:"retrieval_p95_ms"`
	GenerationPopulation   int    `json:"generation_population"`
	GenerationP50MS        *int64 `json:"generation_p50_ms"`
	GenerationP95MS        *int64 `json:"generation_p95_ms"`
}

type Cost struct {
	QueryTokenPopulation int      `json:"query_token_population"`
	InputTokens          *int64   `json:"input_tokens"`
	OutputTokens         *int64   `json:"output_tokens"`
	EmbeddingTokens      *int64   `json:"embedding_input_tokens"`
	QueryCostPopulation  int      `json:"query_cost_population"`
	QueryCostTotal       *float64 `json:"query_cost_total"`
	QueryCostCurrency    string   `json:"query_cost_currency"`
	UnknownQueryCost     int      `json:"unknown_query_cost"`
	MixedQueryCurrencies bool     `json:"mixed_query_currencies"`
}

type QualityTrend struct {
	Day               time.Time `json:"day"`
	DatasetID         string    `json:"dataset_id"`
	DatasetVersion    int       `json:"dataset_version"`
	EvaluatorPolicy   string    `json:"evaluator_policy"`
	CostCurrency      string    `json:"cost_currency"`
	RecallMean        *float64  `json:"recall_mean"`
	MRRMean           *float64  `json:"mrr_mean"`
	NDCGMean          *float64  `json:"ndcg_mean"`
	FaithfulnessMean  *float64  `json:"faithfulness_mean"`
	RelevanceMean     *float64  `json:"relevance_mean"`
	RecallCount       int       `json:"recall_count"`
	MRRCount          int       `json:"mrr_count"`
	NDCGCount         int       `json:"ndcg_count"`
	FaithfulnessCount int       `json:"faithfulness_count"`
	RelevanceCount    int       `json:"relevance_count"`
}

type DailyCost struct {
	Day               time.Time `json:"day"`
	QueryCostTotal    *float64  `json:"query_cost_total"`
	JudgeCostTotal    *float64  `json:"judge_cost_total"`
	QueryCurrency     string    `json:"query_currency"`
	JudgeCurrency     string    `json:"judge_currency"`
	UnknownQueryCosts int       `json:"unknown_query_costs"`
	UnknownJudgeCosts int       `json:"unknown_judge_costs"`
	MixedCurrencies   bool      `json:"mixed_currencies"`
}

type FailureSummary struct {
	Source       string `json:"source"`
	Code         string `json:"code"`
	Count        int    `json:"count"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	DetailPath   string `json:"detail_path"`
	Message      string `json:"message"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func NormalizeFilter(f Filter) Filter {
	now := time.Now().UTC()
	if f.To.IsZero() {
		f.To = now
	}
	if f.From.IsZero() {
		f.From = f.To.Add(-DefaultWindow)
	}
	if f.Timezone == "" {
		f.Timezone = "UTC"
	}
	if f.Traffic == "" {
		f.Traffic = "all"
	}
	return f
}

func trafficClause(alias, traffic string) (string, error) {
	switch traffic {
	case "all":
		return "", nil
	case "query":
		return fmt.Sprintf(" AND %s.request_type='chat'", alias), nil
	case "evaluation":
		return fmt.Sprintf(" AND %s.request_type='evaluation'", alias), nil
	default:
		return "", fmt.Errorf("traffic must be all, query, or evaluation")
	}
}

func (s *Store) Summary(ctx context.Context, filter Filter) (Summary, error) {
	f := NormalizeFilter(filter)
	clause, err := trafficClause("t", f.Traffic)
	if err != nil {
		return Summary{}, err
	}
	out := Summary{Window: Window{From: f.From, To: f.To, Timezone: f.Timezone, Traffic: f.Traffic}, QualityTrends: []QualityTrend{}, DailyExperiment: []DailyCost{}, Failures: []FailureSummary{}}

	var qTotal, qSuccess, ingestOK, ingestFailed, ingestDispatch, evalTotal, evalFailed, providerErrors int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE t.request_type='chat'),
		       count(*) FILTER (WHERE t.request_type='chat' AND t.success),
		       count(*) FILTER (WHERE t.request_type='evaluation'),
		       count(*) FILTER (WHERE t.request_type='evaluation' AND NOT t.success),
		       count(*) FILTER (WHERE t.error_code LIKE 'model_%' OR t.error_code='embedding_failed')
		FROM rag_traces t WHERE t.created_at >= $1 AND t.created_at < $2`+clause,
		f.From, f.To).Scan(&qTotal, &qSuccess, &evalTotal, &evalFailed, &providerErrors); err != nil {
		return Summary{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE state='succeeded'),
		       count(*) FILTER (WHERE state='failed'),
		       count(*) FILTER (WHERE state='dispatch_failed')
		FROM ingestion_jobs WHERE COALESCE(finished_at, updated_at) >= $1 AND COALESCE(finished_at, updated_at) < $2`, f.From, f.To).
		Scan(&ingestOK, &ingestFailed, &ingestDispatch); err != nil {
		return Summary{}, err
	}
	if f.Traffic == "query" {
		evalTotal, evalFailed = 0, 0
	} else if err := s.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE er.status IN ('failed','evaluator_failed'))
		FROM eval_results er WHERE er.created_at >= $1 AND er.created_at < $2`, f.From, f.To).
		Scan(&evalTotal, &evalFailed); err != nil {
		return Summary{}, err
	}
	out.Reliability = Reliability{QueryTotal: qTotal, QuerySuccess: qSuccess, QueryErrors: qTotal - qSuccess,
		IngestionSucceeded: ingestOK, IngestionFailed: ingestFailed, IngestionDispatchFailed: ingestDispatch,
		EvaluationTotal: evalTotal, EvaluationFailed: evalFailed, ProviderErrors: providerErrors}
	if qTotal > 0 {
		rate := float64(qTotal-qSuccess) / float64(qTotal)
		out.Reliability.QueryErrorRate = &rate
	}

	var totalN, retN, genN int
	var totalP50, totalP95, retP50, retP95, genP50, genP95 *float64
	if err := s.pool.QueryRow(ctx, `
		SELECT count(t.total_latency_ms), percentile_cont(0.5) WITHIN GROUP (ORDER BY t.total_latency_ms), percentile_cont(0.95) WITHIN GROUP (ORDER BY t.total_latency_ms)
		FROM rag_traces t WHERE t.success AND t.created_at >= $1 AND t.created_at < $2`+clause, f.From, f.To).
		Scan(&totalN, &totalP50, &totalP95); err != nil {
		return Summary{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT count(s.duration_ms), percentile_cont(0.5) WITHIN GROUP (ORDER BY s.duration_ms), percentile_cont(0.95) WITHIN GROUP (ORDER BY s.duration_ms)
		FROM rag_spans s JOIN rag_traces t ON t.id=s.trace_id
		WHERE s.span_name='retrieval' AND t.created_at >= $1 AND t.created_at < $2`+clause, f.From, f.To).
		Scan(&retN, &retP50, &retP95); err != nil {
		return Summary{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT count(s.duration_ms), percentile_cont(0.5) WITHIN GROUP (ORDER BY s.duration_ms), percentile_cont(0.95) WITHIN GROUP (ORDER BY s.duration_ms)
		FROM rag_spans s JOIN rag_traces t ON t.id=s.trace_id
		WHERE s.span_name='llm_generation' AND t.created_at >= $1 AND t.created_at < $2`+clause, f.From, f.To).
		Scan(&genN, &genP50, &genP95); err != nil {
		return Summary{}, err
	}
	out.Performance = Performance{TotalLatencyPopulation: totalN, TotalLatencyP50MS: roundedPtr(totalP50), TotalLatencyP95MS: roundedPtr(totalP95), RetrievalPopulation: retN, RetrievalP50MS: roundedPtr(retP50), RetrievalP95MS: roundedPtr(retP95), GenerationPopulation: genN, GenerationP50MS: roundedPtr(genP50), GenerationP95MS: roundedPtr(genP95)}

	var tokenN, costN, unknown int
	var costTotal *float64
	var currencyCount int
	var inputTokens, outputTokens, embeddingTokens *int64
	var currency *string
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE t.input_tokens IS NOT NULL OR t.output_tokens IS NOT NULL OR t.embedding_input_tokens IS NOT NULL),
		       sum(t.input_tokens), sum(t.output_tokens), sum(t.embedding_input_tokens),
		       count(t.estimated_cost), sum(t.estimated_cost), count(*) FILTER (WHERE t.success AND t.estimated_cost IS NULL),
		       count(DISTINCT t.cost_currency) FILTER (WHERE t.estimated_cost IS NOT NULL),
		       min(t.cost_currency) FILTER (WHERE t.estimated_cost IS NOT NULL)
		FROM rag_traces t WHERE t.created_at >= $1 AND t.created_at < $2`+clause, f.From, f.To).
		Scan(&tokenN, &inputTokens, &outputTokens, &embeddingTokens, &costN, &costTotal, &unknown, &currencyCount, &currency); err != nil {
		return Summary{}, err
	}
	currencyValue := ""
	if currency != nil {
		currencyValue = *currency
	}
	out.Cost = Cost{QueryTokenPopulation: tokenN, InputTokens: inputTokens, OutputTokens: outputTokens, EmbeddingTokens: embeddingTokens, QueryCostPopulation: costN, QueryCostTotal: costTotal, QueryCostCurrency: currencyValue, UnknownQueryCost: unknown, MixedQueryCurrencies: currencyCount > 1}

	if err := s.qualityTrends(ctx, f, &out); err != nil {
		return Summary{}, err
	}
	if err := s.dailyCosts(ctx, f, &out); err != nil {
		return Summary{}, err
	}
	if err := s.failures(ctx, f, &out); err != nil {
		return Summary{}, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM eval_comparisons WHERE created_at >= $1 AND created_at < $2 AND verdict->'rules' @> '[{"state":"regression"}]'`, f.From, f.To).Scan(&out.RegressionCount); err != nil {
		return Summary{}, err
	}
	return out, nil
}

// roundedPtr rounds percentile_cont's numeric result into integer
// milliseconds while retaining NULL for an empty population.
func roundedPtr(v *float64) *int64 {
	if v == nil {
		return nil
	}
	x := int64(*v + 0.5)
	return &x
}
func (s *Store) qualityTrends(ctx context.Context, f Filter, out *Summary) error {
	if f.Traffic == "query" {
		return nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT date_trunc('day', r.created_at AT TIME ZONE $3), r.dataset_id, r.dataset_version,
		       COALESCE(r.evaluator_policy->>'ndcg_policy_version', r.evaluator_policy->>'policy_version','unknown'),
		       COALESCE(er.cost_currency,'unknown'), avg(er.recall_k), avg(er.mrr), avg(er.ndcg_k),
		       avg(er.groundedness), avg(er.answer_relevance), count(er.recall_k), count(er.mrr), count(er.ndcg_k),
		       count(er.groundedness), count(er.answer_relevance)
		FROM eval_results er JOIN eval_runs r ON r.id=er.run_id
		WHERE er.created_at >= $1 AND er.created_at < $2
		GROUP BY 1,2,3,4,5 ORDER BY 1,2,3`, f.From, f.To, f.Timezone)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var q QualityTrend
		if err := rows.Scan(&q.Day, &q.DatasetID, &q.DatasetVersion, &q.EvaluatorPolicy, &q.CostCurrency, &q.RecallMean, &q.MRRMean, &q.NDCGMean, &q.FaithfulnessMean, &q.RelevanceMean, &q.RecallCount, &q.MRRCount, &q.NDCGCount, &q.FaithfulnessCount, &q.RelevanceCount); err != nil {
			return err
		}
		out.QualityTrends = append(out.QualityTrends, q)
	}
	return rows.Err()
}

func (s *Store) dailyCosts(ctx context.Context, f Filter, out *Summary) error {
	if f.Traffic == "query" {
		return nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT date_trunc('day', r.created_at AT TIME ZONE $3),
		       sum(er.estimated_cost) FILTER (WHERE er.estimated_cost IS NOT NULL),
		       sum(er.evaluator_cost) FILTER (WHERE er.evaluator_cost IS NOT NULL),
		       min(er.cost_currency) FILTER (WHERE er.estimated_cost IS NOT NULL),
		       min(er.evaluator_cost_currency) FILTER (WHERE er.evaluator_cost IS NOT NULL),
		       count(*) FILTER (WHERE er.trace_id IS NOT NULL AND er.estimated_cost IS NULL),
		       count(*) FILTER (WHERE er.trace_id IS NOT NULL AND er.evaluator_cost IS NULL),
		       count(DISTINCT er.cost_currency) FILTER (WHERE er.estimated_cost IS NOT NULL) > 1 OR count(DISTINCT er.evaluator_cost_currency) FILTER (WHERE er.evaluator_cost IS NOT NULL) > 1
		FROM eval_results er JOIN eval_runs r ON r.id=er.run_id
		WHERE r.created_at >= $1 AND r.created_at < $2
		GROUP BY 1 ORDER BY 1`, f.From, f.To, f.Timezone)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d DailyCost
		var queryCurrency, judgeCurrency *string
		if err := rows.Scan(&d.Day, &d.QueryCostTotal, &d.JudgeCostTotal, &queryCurrency, &judgeCurrency, &d.UnknownQueryCosts, &d.UnknownJudgeCosts, &d.MixedCurrencies); err != nil {
			return err
		}
		if queryCurrency != nil {
			d.QueryCurrency = *queryCurrency
		}
		if judgeCurrency != nil {
			d.JudgeCurrency = *judgeCurrency
		}
		out.DailyExperiment = append(out.DailyExperiment, d)
	}
	return rows.Err()
}

func (s *Store) failures(ctx context.Context, f Filter, out *Summary) error {
	queryFilter := ""
	evaluationFilter := ""
	switch f.Traffic {
	case "query":
		queryFilter = " AND t.request_type='chat'"
		evaluationFilter = " AND 1=0"
	case "evaluation":
		queryFilter = " AND 1=0"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT source, code, count(*), resource_type, resource_id, detail_path, max(message)
		FROM (
		 SELECT 'query' source, t.error_code code, t.trace_id resource_id, t.trace_id detail_path,
		        'trace' resource_type, COALESCE(t.error_message,'') message, t.created_at happened_at
		 FROM rag_traces t WHERE NOT t.success`+queryFilter+`
		 UNION ALL
			 SELECT 'ingestion', j.error_code, ir.document_id::text, ir.document_id::text,
			        'document', COALESCE(j.error_message,''), COALESCE(j.finished_at,j.updated_at)
			 FROM ingestion_jobs j
			 JOIN index_revisions ir ON ir.id = j.index_revision_id
			 WHERE j.state IN ('failed','dispatch_failed')
		 UNION ALL
		 SELECT 'evaluation', COALESCE(er.query_error_code,'evaluator_failed'), er.run_id::text,
		        er.run_id::text, 'eval_run', COALESCE(er.query_error_message,er.evaluator_error,''), er.created_at
		 FROM eval_results er
		 WHERE er.status IN ('failed','evaluator_failed')`+evaluationFilter+`
		 UNION ALL
		 SELECT 'evaluation', 'dispatch_failed', r.id::text, r.id::text, 'eval_run',
		        COALESCE(r.dispatch_error,''), r.updated_at
		 FROM eval_runs r WHERE r.status='dispatch_failed'`+evaluationFilter+`
		) failures WHERE happened_at >= $1 AND happened_at < $2
		GROUP BY source, code, resource_type, resource_id, detail_path ORDER BY count(*) DESC LIMIT 100`, f.From, f.To)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item FailureSummary
		if err := rows.Scan(&item.Source, &item.Code, &item.Count, &item.ResourceType, &item.ResourceID, &item.DetailPath, &item.Message); err != nil {
			return err
		}
		if item.ResourceType == "trace" {
			item.DetailPath = "/api/v1/traces/" + item.ResourceID
		}
		if item.ResourceType == "document" {
			item.DetailPath = "/api/v1/documents/" + item.ResourceID
		}
		if item.ResourceType == "eval_run" {
			item.DetailPath = "/api/v1/eval-runs/" + item.ResourceID
		}
		out.Failures = append(out.Failures, item)
	}
	return rows.Err()
}
