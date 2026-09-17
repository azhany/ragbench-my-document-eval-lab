<script setup>
import { onMounted, ref } from 'vue'
import { api } from '../api/client'
import AppIcon from '../components/AppIcon.vue'
import ConfigList from '../components/ConfigList.vue'

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
    if (err.kind === 'http' && err.payload) {
      check.state = 'error'
      check.detail = err.payload
    } else {
      check.state = 'unreachable'
      check.detail = { message: err.message }
    }
  }
}

function metricValue(value, suffix = '') {
  return value == null ? '—' : `${value}${suffix}`
}

onMounted(refresh)
</script>

<template>
  <section class="overview-screen">
    <header class="page-heading">
      <div>
        <p class="eyebrow">Workspace / Overview</p>
        <h2>Document Library</h2>
        <p>Operate your corpus, inspect grounded answers, and keep evaluation evidence close to the run that produced it.</p>
      </div>
      <button type="button" @click="refresh"><AppIcon name="refresh" size="15" /> Refresh status</button>
    </header>

    <section class="overview-hero">
      <article class="welcome-card">
        <div class="hero-icon"><AppIcon name="library" size="23" /></div>
        <div><span class="section-kicker">RAG evaluation workspace</span><h3>Evidence you can follow</h3><p>Every question connects to its retrieved chunks, generated answer, metrics, and persisted trace.</p></div>
        <RouterLink class="primary-link" to="/chat">Open grounded chat <span>→</span></RouterLink>
      </article>
      <article class="health-card">
        <div class="card-title-row"><div><span class="section-kicker">Runtime health</span><h3>Stack status</h3></div><span class="health-ring"><AppIcon name="check" size="16" /></span></div>
        <div class="health-checks">
          <div v-for="check in checks" :key="check.path" class="health-check">
            <span class="health-dot" :data-state="check.state"></span>
            <span>{{ check.name }}</span>
            <strong :data-state="check.state">{{ check.state }}</strong>
          </div>
        </div>
      </article>
    </section>

    <section class="overview-metrics" data-test="overview-metrics">
      <div class="section-head"><div><span class="section-kicker">At a glance</span><h3>Lab summary</h3></div><RouterLink to="/monitor">Open Monitor →</RouterLink></div>
      <p v-if="metricsState === 'loading'" data-state="loading">Loading persisted metrics…</p>
      <p v-else-if="metricsState === 'error'" class="error" data-state="error">Metrics unavailable: {{ metricsError?.message }}</p>
      <p v-else-if="!metrics" data-state="no-data">No metrics summary is available.</p>
      <div v-else class="summary-grid" data-state="ready">
        <article class="summary-card blue"><span>Queries</span><strong>{{ metrics.reliability.query_success }} / {{ metrics.reliability.query_total }}</strong><small>successful / total</small></article>
        <article class="summary-card purple"><span>Latency p95</span><strong>{{ metricValue(metrics.performance.total_latency_p95_ms, ' ms') }}</strong><small>successful traces</small></article>
        <article class="summary-card green"><span>Query cost</span><strong>{{ metricValue(metrics.cost.query_cost_total) }} {{ metrics.cost.query_cost_currency || '' }}</strong><small>{{ metrics.cost.query_cost_population }} costed traces</small></article>
        <article class="summary-card orange"><span>Regressions</span><strong>{{ metrics.regression_count }}</strong><small>persisted comparison verdicts</small></article>
      </div>
    </section>

    <section class="status-panel">
      <div class="section-head"><div><span class="section-kicker">Dependencies</span><h3>Service checks</h3></div><span class="muted">Live API responses</span></div>
      <div class="service-grid">
        <article v-for="check in checks" :key="`${check.path}-detail`" class="service-card">
          <div class="service-card-title"><AppIcon :name="check.state === 'ok' ? 'check' : 'alert'" size="17" /><strong>{{ check.name }}</strong><span :data-state="check.state">{{ check.state }}</span></div>
          <code>{{ check.path }}</code>
          <p>{{ check.state === 'ok' ? 'Responding normally.' : (check.detail?.message || 'Waiting for a response.') }}</p>
        </article>
      </div>
    </section>

    <ConfigList />
  </section>
</template>

<style scoped>
.eyebrow, .section-kicker { margin: 0 0 5px; color: var(--accent); font-size: 10px; font-weight: 800; letter-spacing: .12em; text-transform: uppercase; }
.page-heading h2 { margin: 0; }
.page-heading button { display: inline-flex; align-items: center; gap: 7px; }
.overview-hero { display: grid; grid-template-columns: 1.25fr .75fr; gap: 14px; }
.welcome-card, .health-card, .status-panel { padding: 21px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface); box-shadow: 0 2px 7px rgba(20, 42, 80, .025); }
.welcome-card { display: grid; grid-template-columns: auto 1fr; gap: 15px; align-items: start; background: linear-gradient(135deg, #f3f8ff, #fff 65%); }
.hero-icon { display: grid; width: 44px; height: 44px; place-items: center; border-radius: 12px; color: var(--accent); background: var(--accent-soft); }
.welcome-card h3, .health-card h3, .status-panel h3, .overview-metrics h3 { margin: 0; font-size: 16px; }
.welcome-card p { margin: 7px 0 0; color: var(--muted); }
.primary-link { grid-column: 2; width: fit-content; margin-top: 5px; padding: 8px 12px; border-radius: 7px; color: #fff; background: var(--accent); text-decoration: none; font-size: 11px; font-weight: 700; }
.primary-link span { margin-left: 8px; }
.card-title-row, .section-head { display: flex; align-items: start; justify-content: space-between; gap: 15px; }
.health-ring { display: grid; width: 30px; height: 30px; place-items: center; border-radius: 50%; color: var(--green); background: var(--green-soft); }
.health-checks { display: grid; gap: 12px; margin-top: 19px; }
.health-check { display: grid; grid-template-columns: auto 1fr auto; align-items: center; gap: 8px; color: var(--muted-strong); font-size: 11px; }
.health-check strong { color: var(--green); font-size: 10px; }
.health-check strong[data-state='pending'] { color: var(--muted); }
.health-check strong[data-state='error'], .health-check strong[data-state='unreachable'] { color: var(--danger); }
.health-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--muted); }
.health-dot[data-state='ok'] { background: var(--green); box-shadow: 0 0 0 3px var(--green-soft); }
.health-dot[data-state='error'], .health-dot[data-state='unreachable'] { background: var(--danger); }
.overview-metrics { display: grid; gap: 12px; }
.overview-metrics .section-head { align-items: end; }
.summary-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; }
.summary-card { display: grid; gap: 4px; min-height: 108px; padding: 16px; border: 1px solid var(--border); border-radius: 9px; background: var(--surface); }
.summary-card span { color: var(--muted); font-size: 10px; font-weight: 700; }
.summary-card strong { font-size: 23px; letter-spacing: -.03em; }
.summary-card small { color: var(--muted); font-size: 10px; }
.summary-card.blue { border-top: 3px solid var(--accent); }.summary-card.blue strong { color: var(--accent); }
.summary-card.purple { border-top: 3px solid var(--purple); }.summary-card.purple strong { color: var(--purple); }
.summary-card.green { border-top: 3px solid var(--green); }.summary-card.green strong { color: var(--green); }
.summary-card.orange { border-top: 3px solid var(--orange); }.summary-card.orange strong { color: var(--orange); }
.status-panel { display: grid; gap: 15px; }
.service-grid { display: grid; grid-template-columns: repeat(2, 1fr); gap: 12px; }
.service-card { padding: 13px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface-subtle); }
.service-card-title { display: flex; align-items: center; gap: 7px; }
.service-card-title .app-icon { color: var(--green); }.service-card-title span { margin-left: auto; color: var(--green); font-size: 10px; font-weight: 700; }
.service-card-title span[data-state='pending'] { color: var(--muted); }.service-card-title span[data-state='error'], .service-card-title span[data-state='unreachable'] { color: var(--danger); }
.service-card code { display: inline-block; margin-top: 10px; }.service-card p { margin: 9px 0 0; color: var(--muted); font-size: 11px; }
:deep(.config-list) { margin-top: 0; }
@media (max-width: 900px) { .overview-hero { grid-template-columns: 1fr; }.summary-grid { grid-template-columns: repeat(2, 1fr); } }
@media (max-width: 520px) { .summary-grid, .service-grid { grid-template-columns: 1fr; }.welcome-card { grid-template-columns: auto 1fr; }.primary-link { grid-column: 1 / -1; } }
</style>
