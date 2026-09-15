<script setup>
import { onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'

const route = useRoute()
const router = useRouter()

const configs = ref([])
const configID = ref('')
const question = ref('')
const state = ref('loading')
const error = ref(null)
const errorState = ref('error')
const formError = ref('')
const busy = ref(false)
const turns = ref([])
const traceDetail = ref(null)
const traceLoading = ref(false)
const traceError = ref(null)
let traceLoadSequence = 0

const providerFailureCodes = new Set([
  'embedding_failed',
  'model_timeout',
  'model_rate_limited',
  'model_failed',
  'malformed_response',
  'citation_missing',
  'citation_invalid',
])

function traceIDFromError(err) {
  return err?.trace_id ?? err?.payload?.error?.trace_id ?? ''
}

function classifyError(err) {
  if (err?.code === 'retrieval_empty') return 'retrieval-empty'
  if (providerFailureCodes.has(err?.code)) return 'provider-failure'
  return 'error'
}

async function loadConfigs() {
  state.value = 'loading'
  error.value = null
  try {
    const response = await api.get('/api/v1/rag-configs')
    configs.value = response.configs ?? []
    if (!configID.value && configs.value.length) configID.value = configs.value[0].id
    state.value = configs.value.length ? 'ready' : 'empty-configs'
  } catch (err) {
    error.value = err
    errorState.value = 'error'
    state.value = 'error'
  }
}

async function ask() {
  formError.value = ''
  error.value = null
  const normalizedQuestion = question.value.trim()
  if (!normalizedQuestion) {
    formError.value = 'Enter a question before submitting.'
    return
  }
  if (!configID.value) {
    formError.value = 'Select a saved RAG configuration before submitting.'
    return
  }

  busy.value = true
  try {
    const response = await api.post('/api/v1/chat', {
      question: normalizedQuestion,
      config_id: configID.value,
    })
    const selectedConfig = configs.value.find((config) => config.id === configID.value)
    turns.value.unshift({
      question: normalizedQuestion,
      response,
      // This is the selected config at submission time. Trace detail is the
      // authoritative historical config when a trace is reopened.
      config: selectedConfig ? { ...selectedConfig } : {},
    })
    question.value = ''
  } catch (err) {
    error.value = err
    errorState.value = classifyError(err)
  } finally {
    busy.value = false
  }
}

function displayCost(trace) {
  if (trace?.estimated_cost == null) return 'Unavailable'
  const currency = trace.cost_currency ?? ''
  return `${Number(trace.estimated_cost).toFixed(6)}${currency ? ` ${currency}` : ''}`
}

function displayTokens(value) {
  return value == null ? 'Unavailable' : String(value)
}

function closeTrace() {
  router.replace({ name: 'chat', query: {} })
}

async function loadTrace(id) {
  const requestSequence = ++traceLoadSequence
  if (!id) {
    traceDetail.value = null
    traceError.value = null
    traceLoading.value = false
    return
  }
  traceLoading.value = true
  traceError.value = null
  try {
    // The drawer/panel deliberately gets context_snapshot and config from
    // GET /api/v1/traces/{trace_id}, never from current Library/config state.
    const detail = await api.get(`/api/v1/traces/${encodeURIComponent(id)}`)
    if (requestSequence !== traceLoadSequence) return
    traceDetail.value = detail
  } catch (err) {
    if (requestSequence !== traceLoadSequence) return
    traceDetail.value = null
    traceError.value = err
  } finally {
    if (requestSequence === traceLoadSequence) traceLoading.value = false
  }
}

function traceContext(detail) {
  return detail?.context_snapshot ?? []
}

onMounted(loadConfigs)
watch(() => route.query.trace, (id) => loadTrace(id), { immediate: true })
</script>

<template>
  <section class="chat-screen">
    <div class="section-head">
      <div>
        <h2>Chat</h2>
        <p class="muted">Question → Evidence → Answer → Metrics → Trace</p>
      </div>
    </div>

    <p v-if="state === 'loading'" data-state="loading">Loading saved configurations…</p>

    <section v-else-if="state === 'empty-configs'" class="panel" data-state="empty-configs">
      <h3>No saved configurations</h3>
      <p>Create an immutable RAG configuration before asking a grounded question.</p>
      <RouterLink to="/">Open Overview</RouterLink>
    </section>

    <div v-else>
      <section v-if="error" class="error" :data-state="errorState" role="alert">
        <h3 v-if="errorState === 'retrieval-empty'">No supporting evidence</h3>
        <h3 v-else-if="errorState === 'provider-failure'">Provider request failed</h3>
        <h3 v-else>Chat request unavailable</h3>
        <p>{{ error.message }}</p>
        <RouterLink
          v-if="traceIDFromError(error)"
          :to="{ name: 'chat', query: { trace: traceIDFromError(error) } }"
        >
          Open persisted failure trace
        </RouterLink>
      </section>

      <form class="composer" @submit.prevent="ask">
        <label>
          RAG configuration
          <select v-model="configID" :disabled="busy" aria-label="RAG configuration">
            <option v-for="config in configs" :key="config.id" :value="config.id">
              {{ config.name }} · top-k {{ config.top_k }}
            </option>
          </select>
        </label>
        <label class="question-field">
          Question
          <textarea
            v-model="question"
            rows="4"
            maxlength="2000"
            placeholder="Ask about the processed document library…"
            :disabled="busy"
            required
          />
        </label>
        <button type="submit" :disabled="busy || !configID">
          {{ busy ? 'Asking…' : 'Ask grounded question' }}
        </button>
        <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
      </form>

      <section v-if="turns.length" class="answers" aria-label="Chat answers">
        <article v-for="turn in turns" :key="turn.response.trace.trace_id" class="answer-card">
          <h3>{{ turn.question }}</h3>
          <p class="selected-config">
            Configuration: <strong>{{ turn.config.name || turn.response.trace.trace_id }}</strong>
            <span v-if="turn.config.model_profile"> · {{ turn.config.model_profile }}</span>
          </p>

          <div class="answer" data-state="answer">
            <h4>Answer</h4>
            <p class="answer-text">{{ turn.response.answer }}</p>
          </div>

          <section class="citations" aria-label="Source citations">
            <h4>Evidence citations</h4>
            <p v-if="!turn.response.citations.length" class="muted">The model reported insufficient evidence.</p>
            <ol v-else>
              <li v-for="citation in turn.response.citations" :key="citation.chunk_id" class="citation-card">
                <span class="citation-id">{{ citation.document_id }} / {{ citation.chunk_id }}</span>
                <p>{{ citation.snippet }}</p>
              </li>
            </ol>
          </section>

          <dl class="trace-summary" aria-label="Trace summary">
            <div><dt>Latency</dt><dd>{{ turn.response.trace.latency_ms }} ms</dd></div>
            <div><dt>Input tokens</dt><dd>{{ displayTokens(turn.response.trace.input_tokens) }}</dd></div>
            <div><dt>Output tokens</dt><dd>{{ displayTokens(turn.response.trace.output_tokens) }}</dd></div>
            <div><dt>Query embedding tokens</dt><dd>{{ displayTokens(turn.response.trace.embedding_input_tokens) }}</dd></div>
            <div>
              <dt>Estimated cost</dt>
              <dd :data-state="turn.response.trace.estimated_cost == null ? 'cost-unavailable' : 'cost-available'">
                {{ displayCost(turn.response.trace) }}
                <small v-if="turn.response.trace.cost_unavailable_reason">
                  ({{ turn.response.trace.cost_unavailable_reason }})
                </small>
              </dd>
            </div>
          </dl>

          <p class="trace-link">
            <RouterLink :to="{ name: 'chat', query: { trace: turn.response.trace.trace_id } }">
              Open trace {{ turn.response.trace.trace_id }}
            </RouterLink>
          </p>
        </article>
      </section>
    </div>

    <section v-if="route.query.trace" class="trace-panel" aria-label="Persisted trace detail">
      <div class="trace-panel-head">
        <div>
          <h3>Persisted trace</h3>
          <p class="muted">{{ route.query.trace }}</p>
        </div>
        <button type="button" @click="closeTrace">Close trace</button>
      </div>
      <p v-if="traceLoading" data-state="trace-loading">Loading historical evidence and configuration…</p>
      <div v-else-if="traceError" class="error" data-state="trace-error" role="alert">
        {{ traceError.message }}
      </div>
      <div v-else-if="traceDetail" data-state="trace-ready">
        <p v-if="traceDetail.success" class="trace-status">Successful {{ traceDetail.request_type }} trace</p>
        <p v-else class="trace-status failure">Failed: {{ traceDetail.error_code }}</p>

        <section class="historical-config">
          <h4>Configuration used then</h4>
          <dl>
            <div><dt>Name</dt><dd>{{ traceDetail.config?.name || 'Unavailable' }}</dd></div>
            <div><dt>Config ID</dt><dd>{{ traceDetail.rag_config_id }}</dd></div>
            <div><dt>Embedding</dt><dd>{{ traceDetail.config?.embedding_provider }} / {{ traceDetail.config?.embedding_model }}</dd></div>
            <div><dt>Prompt</dt><dd>{{ traceDetail.prompt_snapshot?.prompt_version || 'Unavailable' }}</dd></div>
          </dl>
        </section>

        <section class="retrieved-context">
          <h4>Retrieved context sent to the model</h4>
          <p v-if="!traceContext(traceDetail).length" class="muted">No context was sent.</p>
          <ol v-else>
            <li v-for="evidence in traceContext(traceDetail)" :key="evidence.chunk_id">
              <strong>#{{ evidence.rank }}</strong>
              <span class="evidence-identity">{{ evidence.document_id }} / {{ evidence.chunk_id }} · revision {{ evidence.revision_id }}</span>
              <p>{{ evidence.content }}</p>
            </li>
          </ol>
        </section>

        <section class="trace-spans">
          <h4>Stages</h4>
          <ul>
            <li v-for="span in traceDetail.spans" :key="`${span.span_name}-${span.started_at}`">
              <strong>{{ span.span_name }}</strong> · {{ span.duration_ms }} ms
            </li>
          </ul>
        </section>
      </div>
    </section>
  </section>
</template>

<style scoped>
.chat-screen { display: grid; gap: 1rem; }
.section-head { display: flex; justify-content: space-between; align-items: center; }
h2 { margin-bottom: .25rem; }
h3, h4 { margin-top: 0; }
.muted, small { color: var(--muted); }
.panel, .composer, .answer-card, .trace-panel { border: 1px solid var(--border); border-radius: .5rem; padding: 1.25rem; background: var(--surface); }
.composer { display: grid; gap: 1rem; }
label { display: grid; gap: .4rem; font-weight: 600; }
select, textarea { font: inherit; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: .35rem; padding: .55rem; }
textarea { resize: vertical; min-height: 6rem; }
.question-field { max-width: 100%; }
button:disabled { opacity: .5; cursor: not-allowed; }
.form-error, .error { color: var(--danger); }
.error { border: 1px solid var(--danger); border-radius: .5rem; padding: 1rem; }
.error h3 { margin-bottom: .4rem; }
.answers { display: grid; gap: 1rem; }
.answer-card { display: grid; gap: 1rem; }
.answer-card h3 { margin-bottom: 0; }
.selected-config { margin: -.7rem 0 0; color: var(--muted); }
.answer, .citations, .historical-config, .retrieved-context, .trace-spans { border-top: 1px solid var(--border); padding-top: 1rem; }
.answer-text { white-space: pre-wrap; overflow-wrap: anywhere; }
.citations ol, .retrieved-context ol { display: grid; gap: .75rem; padding-left: 1.25rem; }
.citation-card { border: 1px solid var(--border); border-radius: .4rem; padding: .75rem; }
.citation-card p, .retrieved-context p { white-space: pre-wrap; overflow-wrap: anywhere; }
.citation-id, .evidence-identity { display: block; color: var(--muted); font: .85rem ui-monospace, SFMono-Regular, Menlo, monospace; overflow-wrap: anywhere; }
.trace-summary, .historical-config dl { display: grid; grid-template-columns: repeat(auto-fit, minmax(9rem, 1fr)); gap: .75rem; margin: 0; }
.trace-summary div, .historical-config dl div { border: 1px solid var(--border); border-radius: .35rem; padding: .65rem; }
dt { color: var(--muted); font-size: .85rem; }
dd { margin: .2rem 0 0; overflow-wrap: anywhere; }
.trace-link { margin-bottom: 0; overflow-wrap: anywhere; }
.trace-panel { display: grid; gap: 1rem; }
.trace-panel-head { display: flex; justify-content: space-between; align-items: start; gap: 1rem; }
.trace-panel-head h3 { margin-bottom: .2rem; }
.trace-status { color: var(--ok); }
.trace-status.failure { color: var(--danger); }
.trace-spans ul { display: grid; gap: .4rem; margin: 0; padding-left: 1.25rem; }
@media (max-width: 650px) {
  .trace-panel-head { display: grid; }
  .trace-summary, .historical-config dl { grid-template-columns: 1fr 1fr; }
}
</style>
