<script setup>
// RB-20: create experiment configs, launch a bounded matrix sweep, compare
// runs against a baseline with the persisted policy, and drill into case
// traces. Quality and efficiency stay separate; incompatible comparisons
// and missing metrics explain themselves instead of becoming one winner.
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'

const configs = ref([])
const datasets = ref([])
const experiments = ref([])
const baseConfigID = ref('')
const datasetID = ref('')
const experimentName = ref('')
const rubricVersion = ref('rubric-v1')
const scoringK = ref(5)
const matrix = ref({
  chunk_sizes: '', chunk_overlaps: '', top_ks: '',
  retrieval_modes: 'vector', prompt_versions: '', model_profiles: '',
})
const rerankRequested = ref(false)
const rerankerProfile = ref('lexical-v1')
const rerankCandidateLimit = ref(20)

const state = ref('loading')
const loadError = ref(null)
const createBusy = ref(false)
const createError = ref(null)
const selected = ref(null)
const detail = ref(null)
const detailError = ref(null)

const candidateRunID = ref('')
const baselineRunID = ref('')
const comparePolicy = ref('default-v1')
const compareBusy = ref(false)
const compare = ref(null)
const compareError = ref(null)

const canCreate = computed(() => baseConfigID.value && datasetID.value && experimentName.value.trim())
const runsOfExperiment = ref([])

const candidateEntry = computed(() =>
  runsOfExperiment.value.find((run) => run.id === candidateRunID.value) ?? null)
const baselineEntry = computed(() =>
  runsOfExperiment.value.find((run) => run.id === baselineRunID.value) ?? null)

function csvList(value) {
  return String(value).split(',').map((s) => s.trim()).filter(Boolean).map(
    (s) => (/^\d+$/.test(s) ? Number(s) : s))
}

function buildMatrix() {
  const m = {}
  for (const [key, value] of Object.entries(matrix.value)) {
    const list = csvList(value)
    if (list.length) m[key] = list
  }
  if (rerankRequested.value) {
    m.rerank_enabled = [true]
    m.reranker_profiles = [rerankerProfile.value]
    m.rerank_candidate_limits = [Number(rerankCandidateLimit.value) || 20]
  }
  return m
}

async function load({ initial = false } = {}) {
  if (initial) state.value = 'loading'
  loadError.value = null
  try {
    const [cfgs, dsList, expList] = await Promise.all([
      api.get('/api/v1/rag-configs'),
      api.get('/api/v1/eval-datasets'),
      api.get('/api/v1/experiments'),
    ])
    configs.value = cfgs.configs ?? []
    datasets.value = dsList.datasets ?? []
    experiments.value = expList.experiments ?? []
    if (!baseConfigID.value && configs.value.length) baseConfigID.value = configs.value[0].id
    if (!datasetID.value && datasets.value.length) datasetID.value = datasets.value[0].id
    state.value = available.value ? 'ready' : 'empty'
  } catch (err) {
    loadError.value = err
    state.value = 'error'
  }
}

const available = computed(() => configs.value.length > 0)

function errorOf(err) {
  return err?.message ?? 'unknown error'
}

async function createExperiment() {
  createError.value = null
  createBusy.value = true
  try {
    // Bounded matrix: the API expands and persists the combinations before
    // execution (with the explicit limit), rejecting invalid/unsupported
    // dimensions, including executable reranking, visibly.
    const created = await api.post('/api/v1/experiments', {
      name: experimentName.value.trim(),
      dataset_id: datasetID.value,
      rubric_version: rubricVersion.value,
      scoring_k: Number(scoringK.value) || 5,
      base_config_id: baseConfigID.value,
      matrix: buildMatrix(),
    })
    selected.value = created
    experimentName.value = ''
    await refresh()
    await open(created.id)
  } catch (err) {
    createError.value = err
  } finally {
    createBusy.value = false
  }
}

async function refresh() {
  const list = await api.get('/api/v1/experiments')
  experiments.value = list.experiments ?? []
}

async function open(id) {
  detailError.value = null
  compare.value = null
  compareError.value = null
  try {
    detail.value = await api.get(`/api/v1/experiments/${id}`)
    const linked = detail.value.combinations.filter((c) => c.eval_run_id)
    runsOfExperiment.value = await Promise.all(linked.map(async (c) => {
      // The experiment detail contains the immutable config/run links, while
      // the run detail contains the pinned corpus and evaluator policy. Load
      // both so the comparison view can show identities instead of only IDs.
      const run = await api.get(`/api/v1/eval-runs/${c.eval_run_id}`).catch(() => null)
      return {
        combination: c.combination_index,
        id: c.eval_run_id,
        status: c.eval_run_status || run?.status || 'unknown',
        configID: c.rag_config_id || run?.rag_config_id || '',
        config: configs.value.find((config) => config.id === (c.rag_config_id || run?.rag_config_id)) ?? null,
        run,
      }
    }))
    if (!candidateRunID.value && runsOfExperiment.value.length) {
      candidateRunID.value = runsOfExperiment.value[0].id
      if (runsOfExperiment.value.length > 1) baselineRunID.value = runsOfExperiment.value[1].id
    }
  } catch (err) {
    detailError.value = err
  }
}

const advanceBusy = ref(false)

async function advance() {
  if (!detail.value) return
  advanceBusy.value = true
  try {
    // One step is idempotent; repeat until terminal.
    let result = { state: '' }
    for (let i = 0; i < 100; i++) {
      result = await api.post(`/api/v1/experiments/${detail.value.id}/advance`, {})
      if (['completed', 'partial', 'failed', 'dispatch_failed'].includes(result.state)) break
    }
    await open(detail.value.id)
  } catch (err) {
    detailError.value = err
  } finally {
    advanceBusy.value = false
  }
}

async function compareNow() {
  compareError.value = null
  compare.value = null
  if (!candidateEntry.value || !baselineEntry.value) {
    compareError.value = { message: 'Select both a candidate and a baseline run before comparing.' }
    return
  }
  compareBusy.value = true
  try {
    const query = `?policy=${encodeURIComponent(comparePolicy.value)}`
    compare.value = await api.get(
      `/api/v1/eval-runs/${candidateRunID.value}/compare/${baselineRunID.value}${query}`)
  } catch (err) {
    compareError.value = err
  } finally {
    compareBusy.value = false
  }
}

const configDiffFields = {
  chunk_size: 'Chunk size',
  chunk_overlap: 'Chunk overlap',
  top_k: 'Top-k',
  retrieval_mode: 'Retrieval mode',
  prompt_version: 'Prompt version',
  model_profile: 'Model profile',
  embedding_profile: 'Embedding profile',
  rerank_enabled: 'Rerank',
  reranker_profile: 'Reranker profile',
  rerank_candidate_limit: 'Rerank candidates',
}

function configValue(entry, field) {
  const value = entry?.config?.[field]
  return value == null ? 'Unavailable' : value
}

const diffRows = computed(() => Object.entries(configDiffFields).map(([field, label]) => ({
  field,
  label,
  baseline: configValue(baselineEntry.value, field),
  candidate: configValue(candidateEntry.value, field),
})))

function revisionIdentity(entry) {
  const revisions = entry?.run?.corpus_revisions
  if (!Array.isArray(revisions) || !revisions.length) return 'No compatible ready revision was pinned'
  return revisions.map((revision) =>
    `${revision.revision_id} · ${revision.filename || revision.document_id}`).join('; ')
}

function policyIdentity(entry) {
  const policy = entry?.run?.evaluator_policy
  if (!policy || typeof policy !== 'object') return 'Unavailable'
  return `${policy.policy_version || 'policy unavailable'} · ${policy.rubric_version || 'rubric unavailable'} · scoring K ${policy.scoring_k ?? 'unavailable'}`
}

function identityFor(entry) {
  return {
    run: entry?.id || 'Unavailable',
    dataset: entry?.run
      ? `${entry.run.dataset_id} · v${entry.run.dataset_version}`
      : 'Unavailable',
    config: entry?.configID || entry?.run?.rag_config_id || 'Unavailable',
    configName: entry?.config?.name || 'Unavailable',
    index: revisionIdentity(entry),
    policy: policyIdentity(entry),
  }
}

const comparisonIdentities = computed(() => [
  { label: 'Baseline', entry: baselineEntry.value, identity: identityFor(baselineEntry.value) },
  { label: 'Candidate', entry: candidateEntry.value, identity: identityFor(candidateEntry.value) },
].filter((item) => item.entry))

const qualityMetrics = new Set(['recall_k', 'mrr', 'ndcg_k'])
function rulesFor(group) {
  return (compare.value?.verdict?.rules ?? []).filter((rule) =>
    group === 'quality' ? qualityMetrics.has(rule.metric) : !qualityMetrics.has(rule.metric))
}

function stateFor(c) {
  if (c.index_error || c.eval_run_error) return 'failed'
  return c.eval_run_status || (c.index_ready ? 'running' : 'created')
}

onMounted(() => load({ initial: true }))
</script>

<template>
  <section class="view" :data-state="state">
    <header>
      <h2>Experiments</h2>
      <p>
        Create configuration variants, launch a bounded matrix sweep, compare a
        candidate against a baseline under the persisted regression policy, and
        drill into each run's case traces.
      </p>
    </header>

    <p v-if="state === 'loading'">Loading…</p>
    <p v-else-if="state === 'error'" data-state="error">
      Cannot reach the API: {{ errorOf(loadError) }}
    </p>
    <p v-else-if="state === 'empty'">
      Create a saved RAG configuration first (see Chat), then come back to
      experiment.
    </p>

    <template v-else data-state="ready">
      <form class="experiment-create" @submit.prevent="createExperiment">
        <label>
          Experiment name
          <input v-model="experimentName" required data-test="experiment-name" />
        </label>
        <label>
          Base configuration (defaults + embedding identity)
          <select v-model="baseConfigID" data-test="base-config">
            <option v-for="c in configs" :key="c.id" :value="c.id">{{ c.name }}</option>
          </select>
        </label>
        <label>
          Golden dataset
          <select v-model="datasetID" data-test="experiment-dataset">
            <option v-for="d in datasets" :key="d.id" :value="d.id">
              {{ d.name }} · v{{ d.latest_version }}
            </option>
          </select>
        </label>
        <label>
          Rubric
          <select v-model="rubricVersion" data-test="experiment-rubric">
            <option value="rubric-v1">rubric-v1</option>
          </select>
        </label>
        <label>
          Scoring K
          <input v-model="scoringK" type="number" min="1" max="100" data-test="experiment-k" />
        </label>
        <fieldset>
          <legend>Configuration matrix (comma-separated values; one per tuned dimension)</legend>
          <label>Chunk sizes
            <input v-model="matrix.chunk_sizes" placeholder="500, 800" data-test="m-chunk-sizes" />
          </label>
          <label>Chunk overlaps
            <input v-model="matrix.chunk_overlaps" placeholder="80, 120" data-test="m-overlaps" />
          </label>
          <label>Top-k
            <input v-model="matrix.top_ks" placeholder="5, 8" data-test="m-topk" />
          </label>
          <label>Retrieval modes
            <input v-model="matrix.retrieval_modes" placeholder="vector, hybrid" data-test="m-modes" />
          </label>
          <label>Prompt versions
            <input v-model="matrix.prompt_versions" placeholder="v1" data-test="m-prompts" />
          </label>
          <label>Model profiles
            <input v-model="matrix.model_profiles" placeholder="openai-gpt-4o-mini" data-test="m-profiles" />
          </label>
          <label><input v-model="rerankRequested" type="checkbox" data-test="rerank-enabled" /> Enable executable reranking</label>
          <label v-if="rerankRequested">Reranker profile
            <select v-model="rerankerProfile" data-test="reranker-profile"><option value="lexical-v1">lexical-v1 · local token overlap</option></select>
          </label>
          <label v-if="rerankRequested">Candidate limit
            <input v-model="rerankCandidateLimit" type="number" min="1" max="100" data-test="rerank-limit" />
          </label>
        </fieldset>
        <button type="submit" :disabled="createBusy || !canCreate" data-test="create-experiment">
          {{ createBusy ? 'Persisting matrix…' : 'Create experiment' }}
        </button>
      </form>
      <p v-if="createError" class="error" data-test="create-error">
        {{ errorOf(createError) }}
      </p>

      <section v-if="experiments.length" class="experiment-list">
        <h3>Experiments</h3>
        <ul data-test="experiment-list">
          <li v-for="e in experiments" :key="e.id">
            <a href="#" @click.prevent="open(e.id)">
              {{ e.name }} ·
              <span :data-status="e.status">{{ e.status }}</span>
            </a>
          </li>
        </ul>
      </section>

      <section v-if="detail" class="experiment-detail" data-test="experiment-detail">
        <h3>{{ detail.name }} <small :data-status="detail.status">{{ detail.status }}</small></h3>
        <button :disabled="advanceBusy" @click="advance" data-test="advance">
          {{ advanceBusy ? 'Advancing…' : 'Advance one step' }}
        </button>

        <table>
          <thead>
            <tr>
              <th>#</th><th>Settings</th><th>Config identity</th><th>Index</th>
              <th>Run</th><th>Run status</th><th>Failure</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="c in detail.combinations" :key="c.combination_index">
              <td>{{ c.combination_index }}</td>
              <td><code>{{ c.settings }}</code></td>
              <td><code>{{ (c.rag_config_id || '—').slice(0, 8) }}</code></td>
              <td>{{ c.index_ready ? 'ready' : (c.index_dispatched ? 'reindexing' : 'not prepared') }}</td>
              <td :data-status="stateFor(c)">
                <a v-if="c.eval_run_id" :href="`/evaluations`">{{ (c.eval_run_id || '').slice(0, 8) }}</a>
                <span v-else>—</span> ({{ stateFor(c) }})
              </td>
              <td class="error" v-if="c.index_error || c.eval_run_error">
                {{ c.index_error || c.eval_run_error }}
              </td>
              <td v-else>—</td>
            </tr>
          </tbody>
        </table>

        <form class="compare" @submit.prevent="compareNow">
          <h4>Compare runs (persisted policy: explicit thresholds, no single winner)</h4>
          <label>Candidate
            <select v-model="candidateRunID" data-test="candidate-run">
              <option v-for="r in runsOfExperiment" :key="r.id" :value="r.id">
                comb. {{ r.combination }} — {{ r.id.slice(0, 8) }} ({{ r.status }})
              </option>
            </select>
          </label>
          <label>Baseline
            <select v-model="baselineRunID" data-test="baseline-run">
              <option v-for="r in runsOfExperiment" :key="r.id" :value="r.id">
                comb. {{ r.combination }} — {{ r.id.slice(0, 8) }} ({{ r.status }})
              </option>
            </select>
          </label>
          <label>Policy
            <select v-model="comparePolicy" data-test="policy">
              <option value="default-v1">default-v1</option>
            </select>
          </label>
          <button type="submit" :disabled="compareBusy" data-test="compare">
            {{ compareBusy ? 'Comparing…' : 'Compare' }}
          </button>
        </form>
        <p v-if="compareError" class="error" data-test="compare-error">
          Comparison unavailable: {{ errorOf(compareError) }}
        </p>

        <section v-if="compare" class="comparison" data-test="comparison">
          <p class="meta">
            compatible: {{ compare.verdict?.comparable }} <span
              v-if="compare.verdict?.reasons?.length">({{ compare.verdict.reasons.join('; ') }})</span>
          </p>
          <p class="muted">Quality and efficiency are reported separately; no single overall winner is inferred.</p>

          <section class="comparison-identities" data-test="comparison-identities">
            <h4>Persisted comparison identities</h4>
            <div class="identity-grid">
              <article v-for="item in comparisonIdentities" :key="item.label" class="identity-card">
                <h5>{{ item.label }}</h5>
                <dl>
                  <dt>Run</dt><dd><code>{{ item.identity.run }}</code></dd>
                  <dt>Dataset/version</dt><dd><code>{{ item.identity.dataset }}</code></dd>
                  <dt>Configuration</dt><dd>{{ item.identity.configName }} · <code>{{ item.identity.config }}</code></dd>
                  <dt>Index revisions</dt><dd><code>{{ item.identity.index }}</code></dd>
                  <dt>Evaluator policy</dt><dd>{{ item.identity.policy }}</dd>
                </dl>
              </article>
            </div>
          </section>

          <table class="config-diff" data-test="config-diff">
            <caption>Configuration differences</caption>
            <thead><tr><th>Setting</th><th>Baseline</th><th>Candidate</th></tr></thead>
            <tbody>
              <tr v-for="row in diffRows" :key="row.field">
                <th scope="row">{{ row.label }}</th>
                <td>{{ row.baseline }}</td>
                <td>{{ row.candidate }}</td>
              </tr>
            </tbody>
          </table>

          <section class="metric-group" data-test="quality-metrics">
            <h4>Quality metrics</h4>
            <table data-test="metric-deltas">
              <thead><tr><th>Metric</th><th>Baseline</th><th>Candidate</th><th>Delta</th><th>Direction</th><th>Threshold</th><th>Verdict</th><th>Reason</th></tr></thead>
              <tbody>
                <tr v-for="rule in rulesFor('quality')" :key="rule.metric" :data-state="rule.state">
                  <td><code>{{ rule.metric }}</code></td>
                  <td>{{ rule.baseline ?? 'missing' }}</td>
                  <td>{{ rule.candidate ?? 'missing' }}</td>
                  <td>{{ rule.delta ?? 'undefined' }}</td>
                  <td>{{ rule.direction }}</td>
                  <td>{{ rule.threshold }}</td>
                  <td>{{ rule.state }}</td>
                  <td>{{ rule.reason }}</td>
                </tr>
              </tbody>
            </table>
          </section>

          <section class="metric-group" data-test="efficiency-metrics">
            <h4>Efficiency metrics</h4>
            <table>
              <thead><tr><th>Metric</th><th>Baseline</th><th>Candidate</th><th>Delta</th><th>Direction</th><th>Threshold</th><th>Verdict</th><th>Reason</th></tr></thead>
              <tbody>
                <tr v-for="rule in rulesFor('efficiency')" :key="rule.metric" :data-state="rule.state">
                  <td><code>{{ rule.metric }}</code></td>
                  <td>{{ rule.baseline ?? 'missing' }}</td>
                  <td>{{ rule.candidate ?? 'missing' }}</td>
                  <td>{{ rule.delta ?? 'undefined' }}</td>
                  <td>{{ rule.direction }}</td>
                  <td>{{ rule.threshold }}</td>
                  <td>{{ rule.state }}</td>
                  <td>{{ rule.reason }}</td>
                </tr>
              </tbody>
            </table>
          </section>
          <p class="note" data-test="delta-units">
            Delta units are stored with each metric (proportions for relative
            deltas, ms for latency, native currency for cost); direction comes
            from the persisted policy row.
          </p>
        </section>
      </section>
      <p v-if="detail && runsOfExperiment.length === 0" data-test="no-runs-yet">
        Advances provision or execute combinations; runs appear here as the
        sweep progresses.
      </p>
    </template>
  </section>
</template>

<style scoped>
.comparison-identities { margin: 1rem 0; }
.identity-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(18rem, 1fr)); gap: 1rem; }
.identity-card { border: 1px solid var(--border); border-radius: .5rem; padding: 1rem; background: var(--surface); }
.identity-card h5 { margin: 0 0 .7rem; }
.identity-card dl { display: grid; grid-template-columns: 8rem 1fr; gap: .45rem .75rem; margin: 0; }
.identity-card dd { margin: 0; overflow-wrap: anywhere; }
.config-diff { margin: 1rem 0; }
.config-diff caption { text-align: left; font-weight: 600; padding: .5rem 0; }
.metric-group { margin: 1rem 0; overflow-x: auto; }
.metric-group h4 { margin-bottom: .5rem; }
</style>
