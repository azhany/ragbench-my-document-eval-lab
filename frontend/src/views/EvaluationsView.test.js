// RB-16 UI tests: dataset/config selection, launch status, distinct
// aggregate cards and per-row errors rendered from persisted state only.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

vi.mock('../api/client', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

import { api } from '../api/client'
import EvaluationsView from './EvaluationsView.vue'

const dataset = { id: 'ds-1', name: 'golden-dataset-v1', latest_version: 3 }
const config = { id: 'cfg-1', name: 'baseline-v1' }
const run = {
  id: 'run-1', dataset_id: 'ds-1', dataset_version: 3, rag_config_id: 'cfg-1',
  status: 'partial', dispatch_error: '',
  aggregate: {
    total_cases: 2, completed: 1, query_failed: 0, evaluator_failed: 1,
    recall_count: 2, recall_mean: 0.75, mrr_count: 2, mrr_mean: 0.5,
    latency_population: 2, latency_p50_ms: 100, latency_p95_ms: 500,
    cost_population: 1, cost_total: 0.02, missing_score_cases: 1,
  },
}
const results = [
  { id: 'r1', eval_case_id: 'c1', case_key: 'qa-001', status: 'completed', trace_id: 'trace-1',
    recall_k: 1, mrr: 1, answer_relevance: 4, groundedness: 5, citation_correct: 1, total_latency_ms: 100 },
  { id: 'r2', eval_case_id: 'c2', case_key: 'qa-002', status: 'evaluator_failed', trace_id: 'trace-2',
    recall_k: 0.5, evaluator_error: 'judge model call failed: 503' },
]

function render(payload) {
  api.get.mockImplementation(async (path) => payload(path))
  api.post.mockReset()
}

afterEach(vi.restoreAllMocks)

describe('EvaluationsView', () => {
  it('keeps the failed-case outcomes visible with distinct aggregate denominators', async () => {
    render((path) => {
      if (path.startsWith('/api/v1/eval-runs/run-1/results')) return { results }
      if (path.startsWith('/api/v1/eval-runs/run-1')) return run
      if (path.startsWith('/api/v1/eval-runs')) return { runs: [run] }
      if (path.startsWith('/api/v1/eval-datasets')) return { datasets: [dataset] }
      return { configs: [config] }
    })
    const wrapper = mount(EvaluationsView)
    await flushPromises()

    expect(wrapper.find('[data-state]').attributes('data-state')).toBe('ready')
    // Open the run detail.
    api.get.mockImplementation(async (path) => {
      if (path.startsWith('/api/v1/eval-runs/run-1/results')) return { results }
      if (path.startsWith('/api/v1/eval-runs/run-1')) return run
      if (path.startsWith('/api/v1/eval-runs')) return { runs: [run] }
      if (path.startsWith('/api/v1/eval-datasets')) return { datasets: [dataset] }
      return { configs: [config] }
    })
    await wrapper.find('[data-test="run-row"]').trigger('click')
    await flushPromises()

    const quality = wrapper.find('[data-test="quality-card"]').text()
    const efficiency = wrapper.find('[data-test="efficiency-card"]').text()
    const failures = wrapper.find('[data-test="failures-card"]').text()
    expect(quality).toContain('Recall@K mean (2')
    expect(efficiency).toContain('Latency p95')
    expect(failures).toContain('Evaluator failed1')
    // Missing score is surfaced, not turned into zero.
    expect(quality).toContain('Missing-score cases')
    // Per-case failure stays visible with its reason; a completed row keeps its trace link.
    const rows = wrapper.findAll('tbody tr')
    expect(rows[0].text()).toContain('completed')
    expect(rows[0].find('[data-test="trace-link"]').exists()).toBe(true)
    expect(rows[1].find('[data-status]').attributes('data-status')).toBe('evaluator_failed')
    expect(rows[1].text()).toContain('judge model call failed: 503')
  })

  it('surfaces an empty state without fabricating data', async () => {
    render((path) => (path.startsWith('/api/v1/eval-datasets')
      ? { datasets: [] } : { configs: [] }))
    const wrapper = mount(EvaluationsView)
    await flushPromises()
    expect(wrapper.find('[data-state="empty"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('seed-golden.py')
  })
})
