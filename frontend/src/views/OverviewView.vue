<script setup>
import { onMounted, ref } from 'vue'
import { api } from '../api/client'
import ConfigList from '../components/ConfigList.vue'

// Health-check state is component-local: nothing else needs it, so no
// shared store is justified (see RB-04 state-locality rule).
const checks = ref([
  { name: 'API liveness', path: '/healthz', state: 'pending', detail: null },
  { name: 'API readiness', path: '/readyz', state: 'pending', detail: null },
])
const metricsState = ref('loading')
const metrics = ref(null)
const metricsError = ref(null)

async function refresh() {
  for (const check of checks.value) {
    check.state = 'pending'
    check.detail = null
  }
  await Promise.all([...checks.value.map(refreshCheck), refreshMetrics()])
}

async function refreshMetrics() {
  metricsState.value = 'loading'
  metricsError.value = null
  try {
    metrics.value = await api.get('/api/v1/metrics/summary?traffic=all&timezone=UTC')
    metricsState.value = 'ready'
  } catch (err) {
    metricsError.value = err
    metricsState.value = 'error'
  }
}

async function refreshCheck(check) {
  try {
    const payload = await api.get(check.path)
    check.state = 'ok'
    check.detail = payload
  } catch (err) {
    // A 503 from /readyz is a meaningful health result, not a client bug:
    // render the payload instead of a generic failure.
    if (err.kind === 'http' && err.payload) {
      check.state = 'error'
      check.detail = err.payload
    } else {
      check.state = 'unreachable'
      check.detail = { message: err.message }
    }
  }
}

onMounted(refresh)
</script>

<template>
  <section>
    <div class="section-head">
      <h2>Stack status</h2>
      <button type="button" @click="refresh">Refresh status</button>
    </div>
    <div class="cards">
      <article v-for="check in checks" :key="check.path" class="card">
        <h3>{{ check.name }} — <code>{{ check.path }}</code></h3>
        <p class="state" :data-state="check.state">
          {{ check.state }}
        </p>
        <pre>{{ JSON.stringify(check.detail, null, 2) }}</pre>
      </article>
    </div>
  </section>

  <ConfigList />

  <section class="overview-metrics" data-test="overview-metrics">
    <div class="section-head"><h2>PoC summary</h2><RouterLink to="/monitor">Open Monitor</RouterLink></div>
    <p v-if="metricsState === 'loading'" data-state="loading">Loading persisted metrics…</p>
    <p v-else-if="metricsState === 'error'" class="error" data-state="error">Metrics unavailable: {{ metricsError?.message }}</p>
    <p v-else-if="!metrics" data-state="no-data">No metrics summary is available.</p>
    <div v-else class="cards" data-state="ready">
      <article class="card"><h3>Queries</h3><strong>{{ metrics.reliability.query_success }} / {{ metrics.reliability.query_total }}</strong><p>successful / total</p></article>
      <article class="card"><h3>Latency p95</h3><strong>{{ metrics.performance.total_latency_p95_ms ?? 'Unavailable' }}{{ metrics.performance.total_latency_p95_ms == null ? '' : ' ms' }}</strong><p>successful traces</p></article>
      <article class="card"><h3>Query cost</h3><strong>{{ metrics.cost.query_cost_total ?? 'Unavailable' }} {{ metrics.cost.query_cost_currency }}</strong><p>{{ metrics.cost.query_cost_population }} costed traces</p></article>
      <article class="card"><h3>Regressions</h3><strong>{{ metrics.regression_count }}</strong><p>persisted comparison verdicts</p></article>
    </div>
  </section>
</template>

<style scoped>
.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}
h2 {
  margin: 1.5rem 0 0.75rem;
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr));
  gap: 1rem;
}
.card {
  border: 1px solid var(--border);
  border-radius: 0.5rem;
  padding: 0.9rem 1rem;
  background: var(--surface);
}
.card h3 {
  margin: 0 0 0.5rem;
  font-size: 0.95rem;
  font-weight: 600;
}
.state {
  margin: 0 0 0.5rem;
  font-weight: 600;
}
.state[data-state='ok'] {
  color: var(--ok);
}
.state[data-state='error'],
.state[data-state='unreachable'] {
  color: var(--danger);
}
pre {
  background: var(--code-bg);
  border-radius: 0.4rem;
  padding: 0.6rem;
  overflow-x: auto;
  font-size: 0.8rem;
  margin: 0;
}
.overview-metrics { margin-top: 2rem; }
.overview-metrics .cards { display:grid; grid-template-columns:repeat(auto-fit,minmax(12rem,1fr)); gap:1rem; }
.overview-metrics .card { border:1px solid var(--border); border-radius:.5rem; padding:1rem; background:var(--surface); }
.overview-metrics .card h3 { margin-top:0; }
.overview-metrics strong { font-size:1.35rem; }
.overview-metrics p { margin-bottom:0; color:var(--muted); }
.error { color:var(--danger); }
</style>
