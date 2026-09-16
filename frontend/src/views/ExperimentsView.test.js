// RB-20 UI tests: the comparison is driven by persisted run/config identities
// and keeps quality and efficiency verdicts separate.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

vi.mock('../api/client', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

import { api } from '../api/client'
import ExperimentsView from './ExperimentsView.vue'

const baseline = {
  id: 'cfg-baseline', name: 'baseline-config', chunk_size: 800, chunk_overlap: 120,
  top_k: 5, retrieval_mode: 'vector', prompt_version: 'v1', model_profile: 'model-a',
  embedding_profile: 'embedding-a', rerank_enabled: false,
  reranker_profile: 'lexical-v1', rerank_candidate_limit: 20,
}
const candidate = {
  ...baseline, id: 'cfg-candidate', name: 'candidate-config', chunk_size: 400,
  top_k: 2, retrieval_mode: 'hybrid', model_profile: 'model-b',
}
const experiment = {
  id: 'experiment-1', name: 'two-config-demo', status: 'completed',
  combinations: null,
}
const detail = {
  ...experiment,
  dataset_id: 'dataset-1', dataset_version: 1,
  combinations: [
    {
      combination_index: 1, settings: { chunk_size: 800 }, rag_config_id: baseline.id,
      index_ready: true, eval_run_id: 'run-baseline', eval_run_status: 'completed',
    },
    {
      combination_index: 2, settings: { chunk_size: 400 }, rag_config_id: candidate.id,
      index_ready: true, eval_run_id: 'run-candidate', eval_run_status: 'completed',
    },
  ],
}
const run = (id, configID, revisionID) => ({
  id, dataset_id: 'dataset-1', dataset_version: 1, rag_config_id: configID,
  corpus_revisions: [{ revision_id: revisionID, filename: 'source.txt' }],
  evaluator_policy: { policy_version: 'evaluator-policy-v1', rubric_version: 'rubric-v1', scoring_k: 5 },
  status: 'completed',
})

afterEach(() => vi.restoreAllMocks())

function installAPI() {
  api.get.mockImplementation(async (path) => {
    if (path === '/api/v1/rag-configs') return { configs: [baseline, candidate] }
    if (path === '/api/v1/eval-datasets') return { datasets: [{ id: 'dataset-1', name: 'golden', latest_version: 1 }] }
    if (path === '/api/v1/experiments') return { experiments: [experiment] }
    if (path === '/api/v1/experiments/experiment-1') return detail
    if (path === '/api/v1/eval-runs/run-baseline') return run('run-baseline', baseline.id, 'revision-baseline')
    if (path === '/api/v1/eval-runs/run-candidate') return run('run-candidate', candidate.id, 'revision-candidate')
    if (path.startsWith('/api/v1/eval-runs/run-candidate/compare/run-baseline')) {
      return {
        verdict: {
          comparable: true,
          rules: [
            { metric: 'recall_k', baseline: 0.8, candidate: 0.7, delta: -0.1, direction: 'higher', threshold: 0.05, state: 'regression', reason: 'recall dropped' },
            { metric: 'latency_p95', baseline: 100, candidate: 110, delta: 10, direction: 'lower', threshold: 0.2, state: 'passed', reason: 'within threshold' },
          ],
        },
      }
    }
    return {}
  })
}

describe('ExperimentsView', () => {
  it('shows real combination numbers, both persisted identities, and separate metric groups', async () => {
    installAPI()
    const wrapper = mount(ExperimentsView)
    await flushPromises()

    await wrapper.find('[data-test="experiment-list"] a').trigger('click')
    await flushPromises()

    const runOptions = wrapper.find('[data-test="candidate-run"]').findAll('option')
    expect(runOptions[0].text()).toContain('comb. 1')
    expect(runOptions[1].text()).toContain('comb. 2')
    expect(runOptions[0].text()).not.toContain('undefined')

    await wrapper.find('[data-test="candidate-run"]').setValue('run-candidate')
    await wrapper.find('[data-test="baseline-run"]').setValue('run-baseline')
    await wrapper.find('form.compare').trigger('submit')
    await flushPromises()

    const identities = wrapper.find('[data-test="comparison-identities"]').text()
    expect(identities).toContain('baseline-config')
    expect(identities).toContain('candidate-config')
    expect(identities).toContain('revision-baseline')
    expect(identities).toContain('revision-candidate')
    expect(identities).toContain('evaluator-policy-v1')

    const diff = wrapper.find('[data-test="config-diff"]').text()
    expect(diff).toContain('800')
    expect(diff).toContain('400')
    expect(wrapper.find('[data-test="quality-metrics"]').text()).toContain('recall_k')
    expect(wrapper.find('[data-test="efficiency-metrics"]').text()).toContain('latency_p95')
    expect(wrapper.text()).toContain('no single overall winner')
  })
})
