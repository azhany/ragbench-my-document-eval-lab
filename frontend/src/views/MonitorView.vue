<script setup>
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'

const state = ref('loading')
const error = ref(null)
const summary = ref(null)
const traces = ref([])
const trace = ref(null)
const traceError = ref(null)
const from = ref('')
const to = ref('')
const timezone = ref(Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC')
const traffic = ref('all')

const unavailable = (value) => value == null ? 'Unavailable' : value
const errorMessage = (value) => value?.message || 'unknown error'
const metricURL = computed(() => {
  const params = new URLSearchParams({ traffic: traffic.value, timezone: timezone.value })
  if (from.value) params.set('from', new Date(`${from.value}T00:00:00Z`).toISOString())
  if (to.value) params.set('to', new Date(`${to.value}T23:59:59Z`).toISOString())
  return `/api/v1/metrics/summary?${params}`
})

async function load() {
  state.value = 'loading'
  error.value = null
  try {
    const [metrics, traceList] = await Promise.all([
      api.get(metricURL.value), api.get('/api/v1/traces?limit=50'),
    ])
    summary.value = metrics
    traces.value = traceList.traces ?? []
    state.value = 'ready'
  } catch (err) {
    error.value = err
    state.value = 'error'
  }
}

async function openTrace(item) {
  traceError.value = null
  try { trace.value = await api.get(`/api/v1/traces/${encodeURIComponent(item.trace_id)}`) }
  catch (err) { trace.value = null; traceError.value = err }
}

onMounted(load)
</script>

<template>
  <section class="view monitor" :data-state="state">
    <header>
      <h2>Monitor</h2>
      <p>Database-backed reliability, performance, cost and quality evidence. Missing usage and empty populations remain unavailable.</p>
    </header>
    <form class="filters" @submit.prevent="load">
      <label>From <input v-model="from" type="date" /></label>
      <label>To <input v-model="to" type="date" /></label>
      <label>Traffic
        <select v-model="traffic"><option value="all">All</option><option value="query">Chat/query</option><option value="evaluation">Evaluation</option></select>
      </label>
      <label>Timezone <input v-model="timezone" placeholder="Asia/Kuala_Lumpur" /></label>
      <button type="submit">Apply filters</button>
    </form>
    <p class="muted">Window and units come from <code>GET /api/v1/metrics/summary</code>; timezone: {{ timezone }} · traffic: {{ traffic }}</p>

    <p v-if="state === 'loading'" data-state="loading">Loading metrics…</p>
    <section v-else-if="state === 'error'" class="panel error" data-state="error">
      <strong>Metrics unavailable.</strong> {{ errorMessage(error) }} <button type="button" @click="load">Retry</button>
    </section>
    <template v-else>
      <section class="cards" data-test="metric-cards">
        <article class="card"><h3>Query reliability</h3><dl>
          <dt>Success / total</dt><dd>{{ summary.reliability.query_success }} / {{ summary.reliability.query_total }}</dd>
          <dt>Error rate</dt><dd>{{ summary.reliability.query_error_rate == null ? 'Unavailable' : `${(summary.reliability.query_error_rate * 100).toFixed(1)}%` }}</dd>
          <dt>Provider errors</dt><dd>{{ summary.reliability.provider_errors }}</dd>
        </dl></article>
        <article class="card"><h3>Latency</h3><dl>
          <dt>Total p50</dt><dd>{{ unavailable(summary.performance.total_latency_p50_ms) }} ms</dd>
          <dt>Total p95</dt><dd>{{ unavailable(summary.performance.total_latency_p95_ms) }} ms</dd>
          <dt>Retrieval p95</dt><dd>{{ unavailable(summary.performance.retrieval_p95_ms) }} ms</dd>
          <dt>Generation p95</dt><dd>{{ unavailable(summary.performance.generation_p95_ms) }} ms</dd>
        </dl></article>
        <article class="card"><h3>Tokens and cost</h3><dl>
          <dt>Input tokens</dt><dd>{{ unavailable(summary.cost.input_tokens) }}</dd>
          <dt>Output tokens</dt><dd>{{ unavailable(summary.cost.output_tokens) }}</dd>
          <dt>Cost/query total</dt><dd>{{ unavailable(summary.cost.query_cost_total) }} {{ summary.cost.query_cost_currency }}</dd>
          <dt>Unknown cost</dt><dd>{{ summary.cost.unknown_query_cost }}</dd>
        </dl></article>
        <article class="card"><h3>Quality</h3><dl>
          <dt>Trend groups</dt><dd>{{ summary.quality_trends.length }}</dd>
          <dt>Regressions</dt><dd>{{ summary.regression_count }}</dd>
          <dt>Ingestion failures</dt><dd>{{ summary.reliability.ingestion_failed }}</dd>
          <dt>Evaluation failures</dt><dd>{{ summary.reliability.evaluation_failed }}</dd>
        </dl></article>
      </section>

      <section class="panel" data-test="quality-trends"><h3>Quality trends</h3>
        <p v-if="!summary.quality_trends.length" data-state="no-data">No compatible evaluation quality data in this window.</p>
        <div v-else class="table-scroll"><table><thead><tr><th>Day</th><th>Dataset/version</th><th>Policy</th><th>Recall@K</th><th>MRR</th><th>nDCG@K</th><th>Faithfulness</th><th>Relevance</th></tr></thead>
          <tbody><tr v-for="trend in summary.quality_trends" :key="`${trend.day}-${trend.dataset_id}-${trend.dataset_version}-${trend.evaluator_policy}-${trend.cost_currency}`"><td>{{ trend.day }}</td><td>{{ trend.dataset_id.slice(0, 8) }} / v{{ trend.dataset_version }}</td><td>{{ trend.evaluator_policy }}</td><td>{{ unavailable(trend.recall_mean) }}</td><td>{{ unavailable(trend.mrr_mean) }}</td><td>{{ unavailable(trend.ndcg_mean) }}</td><td>{{ unavailable(trend.faithfulness_mean) }}</td><td>{{ unavailable(trend.relevance_mean) }}</td></tr></tbody>
        </table></div>
      </section>

      <section class="panel" data-test="failures"><h3>Recent failures</h3>
        <p v-if="!summary.failures.length" data-state="no-data">No failures in this window.</p>
        <ul v-else><li v-for="failure in summary.failures" :key="`${failure.source}-${failure.code}-${failure.resource_id}`"><strong>{{ failure.code }}</strong> ×{{ failure.count }} · {{ failure.message }} <a :href="failure.detail_path">Open {{ failure.resource_type }}</a></li></ul>
      </section>

      <section class="panel" data-test="recent-traces"><h3>Recent traces</h3>
        <p v-if="!traces.length" data-state="no-data">No traces in this window.</p>
        <ul v-else><li v-for="item in traces" :key="item.trace_id"><button type="button" class="link-button" @click="openTrace(item)">{{ item.trace_id }}</button> · {{ item.request_type }} · {{ item.success ? 'success' : item.error_code }} · {{ item.total_latency_ms }} ms</li></ul>
      </section>
      <section v-if="trace" class="panel" data-test="trace-detail"><h3>Trace evidence and spans</h3><p>{{ trace.question }}</p><p v-if="trace.error_code" class="error">{{ trace.error_code }}: {{ trace.error_message }}</p><ol><li v-for="span in trace.spans" :key="`${span.span_name}-${span.started_at}`"><code>{{ span.span_name }}</code> — {{ span.duration_ms }} ms <pre>{{ JSON.stringify(span.metadata, null, 2) }}</pre></li></ol></section>
      <p v-if="traceError" class="error">Trace unavailable: {{ errorMessage(traceError) }}</p>
    </template>
  </section>
</template>

<style scoped>
.filters { display:flex; flex-wrap:wrap; gap:1rem; align-items:end; padding:1rem; border:1px solid var(--border); border-radius:.5rem; }
label { display:grid; gap:.35rem; }
input, select { font:inherit; padding:.35rem; }
.cards { display:grid; grid-template-columns:repeat(auto-fit,minmax(15rem,1fr)); gap:1rem; margin:1rem 0; }
.card, .panel { border:1px solid var(--border); border-radius:.5rem; padding:1rem; margin:1rem 0; background:var(--surface); }
.card h3, .panel h3 { margin-top:0; }
dl { display:grid; grid-template-columns:1fr auto; gap:.4rem .8rem; }
dd { margin:0; font-weight:600; }
table { width:100%; border-collapse:collapse; }
th, td { text-align:left; padding:.55rem; border-bottom:1px solid var(--border); vertical-align:top; }
.table-scroll { overflow-x:auto; }
.muted { color:var(--muted); }
.error { border-color:var(--danger); color:var(--danger); }
.link-button { border:0; padding:0; color:var(--accent); background:none; }
pre { max-width:35rem; overflow:auto; background:var(--code-bg); padding:.4rem; }
li { margin:.45rem 0; }
</style>
