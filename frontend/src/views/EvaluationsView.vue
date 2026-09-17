<script setup>
// RB-16: run and inspect evaluations without database access.
// Datasets and configs come from the API; a launched run reaches its real
// terminal/partial state (no fake progress): status comes from the run
// record, aggregates keep quality/efficiency/failures separate, and each
// case rows links its trace. Invalid dataset, failed launch, missing
// scores and failed cases stay visible after reload because everything is
// rendered from persisted state.
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'

const datasets = ref([])
const configs = ref([])
const datasetID = ref('')
const datasetVersion = ref(0)
const configID = ref('')
const rubricVersion = ref('rubric-v1')
const scoringK = ref(5)

const state = ref('loading')
const loadError = ref(null)
const launchBusy = ref(false)
const launchError = ref(null)
const runs = ref([])
const selectedRun = ref(null)
const runDetail = ref(null)
const runResults = ref([])
const runError = ref(null)

const step = (message) => `${message}`

const selectedDataset = computed(() =>
  datasets.value.find((d) => d.id === datasetID.value) ?? null)

const available = computed(() => datasets.value.length > 0 && configs.value.length > 0)

async function load({ initial = false } = {}) {
  if (initial) state.value = 'loading'
  loadError.value = null
  try {
    const [dsList, cfgList, runList] = await Promise.all([
      api.get('/api/v1/eval-datasets'),
      api.get('/api/v1/rag-configs'),
      api.get('/api/v1/eval-runs'),
    ])
    datasets.value = dsList.datasets ?? []
    configs.value = cfgList.configs ?? []
    runs.value = runList.runs ?? []
    if (!datasetID.value && datasets.value.length) datasetID.value = datasets.value[0].id
    if (!configID.value && configs.value.length) configID.value = configs.value[0].id
    state.value = available.value ? 'ready' : 'empty'
  } catch (err) {
    loadError.value = err
    state.value = 'error'
  }
}

async function importDataset(event) {
  launchError.value = null
  try {
    const file = event.target.files?.[0]
    if (!file) return
    const text = await file.text()
    // The import contract accepts the exact dataset payload; an invalid
    // dataset (unknown documents, duplicate keys) surfaces its reason.
    await api.post('/api/v1/eval-datasets', JSON.parse(text))
    event.target.value = ''
    await load()
  } catch (err) {
    launchError.value = err
  }
}

async function launch() {
  launchError.value = null
  launchBusy.value = true
  try {
    const created = await api.post('/api/v1/eval-runs', {
      dataset_id: datasetID.value,
      dataset_version: Number(datasetVersion.value) || 0,
      rag_config_id: configID.value,
      rubric_version: rubricVersion.value,
      scoring_k: Number(scoringK.value) || 5,
    })
    selectedRun.value = created
    await refreshRuns(selectedRun.value.id)
  } catch (err) {
    launchError.value = err
  } finally {
    launchBusy.value = false
  }
}

async function refreshRuns(selectId) {
  const list = await api.get('/api/v1/eval-runs')
  runs.value = list.runs ?? []
  const id = selectId ?? selectedRun.value?.id
  if (id) await selectRun(runs.value.find((r) => r.id === id) ?? runs.value[0])
}

async function selectRun(run) {
  runError.value = null
  selectedRun.value = run
  try {
    const [detail, results] = await Promise.all([
      api.get(`/api/v1/eval-runs/${run.id}`),
      api.get(`/api/v1/eval-runs/${run.id}/results`),
    ])
    runDetail.value = detail
    runResults.value = results.results ?? []
  } catch (err) {
    runError.value = err
  }
}

const aggregate = computed(() => runDetail.value?.aggregate ?? {})

function errorOf(err) {
  return err?.message ?? step('unknown error')
}

onMounted(() => load({ initial: true }))
</script>

<template>
  <section class="view" :data-state="state">
    <header>
      <h2>Evaluations</h2>
      <p>
        Select a versioned golden dataset and a saved configuration, launch the
        run through the real query pipeline, and inspect per-case scores,
        failures and traces.
      </p>
    </header>

    <p v-if="state === 'loading'" data-state="loading">Loading datasets…</p>
    <p v-else-if="state === 'error'" data-state="error">
      Cannot reach the API: {{ errorOf(loadError) }}
    </p>
    <p v-else-if="state === 'empty'" data-state="empty">
      No golden dataset yet. Import one (see
      <code>scripts/seed-golden.py</code>) and create a saved configuration in
      Settings first.
    </p>

    <template v-else data-state="ready">
      <form class="run-launch" @submit.prevent="launch">
        <label>
          Golden dataset
          <select v-model="datasetID" data-test="dataset-selector">
            <option v-for="d in datasets" :key="d.id" :value="d.id">
              {{ d.name }} · v{{ d.latest_version }}
            </option>
          </select>
        </label>
        <label>
          Version
          <input v-model="datasetVersion" type="number" min="0" :max="selectedDataset?.latest_version ?? 0"
                 placeholder="latest" data-test="dataset-version" />
        </label>
        <label>
          Configuration
          <select v-model="configID" data-test="config-selector">
            <option v-for="c in configs" :key="c.id" :value="c.id">{{ c.name }}</option>
          </select>
        </label>
        <label>
          Judge rubric
          <select v-model="rubricVersion" data-test="rubric-selector">
            <option value="rubric-v1">rubric-v1</option>
          </select>
        </label>
        <label>
          Scoring K
          <input v-model="scoringK" type="number" min="1" max="100" data-test="scoring-k" />
        </label>
        <button type="submit" :disabled="launchBusy || !available" data-test="launch">
          {{ launchBusy ? 'Launching…' : 'Launch run' }}
        </button>
      </form>
      <p v-if="launchError" class="error" data-test="launch-error" data-state="launch-error">
        Launch failed: {{ errorOf(launchError) }}
      </p>
      <p v-if="runError" class="error" data-test="run-error">
        Run inspection failed: {{ errorOf(runError) }}
      </p>

      <section v-if="runs.length" class="run-list" data-test="run-list">
        <h3>Runs</h3>
        <ul>
          <li v-for="run in runs" :key="run.id" :class="{ selected: run.id === selectedRun?.id }">
            <a href="#" @click.prevent="selectRun(run)" data-test="run-row">
              <code>{{ run.id.slice(0, 8) }}</code>
              <span class="status" :data-status="run.status">{{ run.status }}</span>
              <span v-if="run.dispatch_error" class="error" data-test="dispatch-error">
                dispatch: {{ run.dispatch_error }}
              </span>
            </a>
          </li>
        </ul>
      </section>

      <section v-if="runDetail" class="run-detail" data-test="run-detail">
        <h3>Run <code>{{ runDetail.id }}</code></h3>
        <p class="meta">
          dataset {{ runDetail.dataset_name || runDetail.dataset_id }} ·
          version {{ runDetail.dataset_version }} · config
          {{ runDetail.rag_config_id }} · status
          <strong :data-status="runDetail.status">{{ runDetail.status }}</strong>
          <span v-if="runDetail.dispatch_error">
            (dispatch failure: {{ runDetail.dispatch_error }})
          </span>
        </p>

        <div class="aggregate-cards" data-test="aggregate-cards">
          <div class="card" data-test="quality-card">
            <h4>Quality</h4>
            <dl>
              <dt>Recall@K mean ({{ aggregate?.recall_count ?? 0 }} scored)</dt>
              <dd>{{ aggregate?.recall_mean ?? 'missing' }}</dd>
              <dt>MRR mean ({{ aggregate?.mrr_count ?? 0 }} scored)</dt>
              <dd>{{ aggregate?.mrr_mean ?? 'missing' }}</dd>
              <dt>nDCG@K mean ({{ aggregate?.ndcg_count ?? 0 }} graded)</dt>
              <dd>{{ aggregate?.ndcg_mean ?? 'missing' }}</dd>
              <dt>Answer relevance mean ({{ aggregate?.answer_relevance_count ?? 0 }})</dt>
              <dd>{{ aggregate?.answer_relevance_mean ?? 'missing' }}</dd>
              <dt>Groundedness mean ({{ aggregate?.groundedness_count ?? 0 }})</dt>
              <dd>{{ aggregate?.groundedness_mean ?? 'missing' }}</dd>
              <dt>Missing-score cases</dt>
              <dd>{{ aggregate?.missing_score_cases ?? 0 }}</dd>
            </dl>
          </div>
          <div class="card" data-test="efficiency-card">
            <h4>Efficiency</h4>
            <dl>
              <dt>Latency p50 ({{ aggregate?.latency_population ?? 0 }} completed)</dt>
              <dd>{{ aggregate?.latency_p50_ms != null ? aggregate.latency_p50_ms + ' ms' : 'missing' }}</dd>
              <dt>Latency p95</dt>
              <dd>{{ aggregate?.latency_p95_ms != null ? aggregate.latency_p95_ms + ' ms' : 'missing' }}</dd>
              <dt>Evaluated cost total ({{ aggregate?.cost_population ?? 0 }} costed)</dt>
              <dd>{{ aggregate?.cost_total ?? 'missing' }}</dd>
            </dl>
          </div>
          <div class="card" data-test="failures-card">
            <h4>Failures</h4>
            <dl>
              <dt>Completed</dt><dd>{{ aggregate?.completed ?? 0 }}</dd>
              <dt>Query failed</dt><dd>{{ aggregate?.query_failed ?? 0 }}</dd>
              <dt>Evaluator failed</dt><dd>{{ aggregate?.evaluator_failed ?? 0 }}</dd>
              <dt>Total cases</dt><dd>{{ aggregate?.total_cases ?? '?' }}</dd>
            </dl>
          </div>
        </div>

        <table class="results" data-test="results">
          <thead>
            <tr>
              <th>Case</th><th>Status</th><th>Recall@K</th><th>MRR</th><th>nDCG@K</th>
              <th>Relevance</th><th>Groundedness</th><th>Citations</th>
              <th>Latency</th><th>Trace</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in runResults" :key="r.id">
              <td><code>{{ r.case_key }}</code></td>
              <td :data-status="r.status">{{ r.status }}</td>
              <td>{{ r.recall_k ?? '—' }}</td>
              <td>{{ r.mrr ?? '—' }}</td>
              <td>{{ r.ndcg_k ?? '—' }}</td>
              <td :title="r.answer_relevance_rationale">{{ r.answer_relevance ?? 'missing' }}</td>
              <td :title="r.groundedness_rationale">{{ r.groundedness ?? 'missing' }}</td>
              <td>{{ r.citation_correct ?? '—' }}</td>
              <td>{{ r.total_latency_ms ?? '—' }} ms</td>
              <td>
                <a v-if="r.trace_id" :href="`/chat?trace=${r.trace_id}`" data-test="trace-link">
                  {{ r.trace_id.slice(0, 8) }}
                </a>
                <span v-else-if="r.query_error_code" class="error">
                  {{ r.query_error_code }}: {{ r.query_error_message }}
                </span>
                <span v-else>—</span>
                <span v-if="r.evaluator_error" class="error" data-test="evaluator-error">
                  {{ r.evaluator_error }}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </section>
      <p v-if="!runDetail && runs.length" data-test="no-run-selected">Select a run to inspect it.</p>
    </template>
  </section>
</template>
