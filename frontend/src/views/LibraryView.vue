<script setup>
import { onMounted, onUnmounted, ref } from 'vue'
import { api } from '../api/client'

const documents = ref([])
const configs = ref([])
const configID = ref('')
const file = ref(null)
const fileInput = ref(null)
const state = ref('loading')
const error = ref(null)
const notice = ref('')
const busy = ref(false)
const selected = ref(null)
const deleting = ref(null)
let timer
let disposed = false
let refreshing = false

async function refresh(initial = false) {
  if (refreshing || disposed) return
  refreshing = true
  try {
    const [library, settings] = await Promise.all([
      api.get('/api/v1/documents'), api.get('/api/v1/rag-configs'),
    ])
    if (disposed) return
    documents.value = library.documents
    configs.value = settings.configs
    if (!configID.value && configs.value.length) configID.value = configs.value[0].id
    if (selected.value) selected.value = await api.get(`/api/v1/documents/${selected.value.id}`).catch(() => null)
    state.value = documents.value.length ? 'ready' : 'empty'
    if (initial) error.value = null
  } catch (err) {
    error.value = err
    state.value = 'error'
  } finally {
    refreshing = false
    clearTimeout(timer)
    // Failed tasks can recover on an Airflow retry; keep observing those too.
    if (!disposed && documents.value.length) timer = setTimeout(() => refresh(), 5000)
  }
}

async function act(operation, message) {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await operation()
    notice.value = message
  } catch (err) {
    error.value = err
  } finally {
    busy.value = false
    await refresh()
  }
}

async function upload() {
  if (!file.value || !configID.value) return
  const body = new FormData()
  body.append('file', file.value)
  body.append('config_id', configID.value)
  await act(async () => {
    await api.post('/api/v1/documents', body)
    file.value = null
    fileInput.value.value = ''
  }, 'Document queued for processing.')
}

function reprocess(doc) {
  return act(() => api.post(`/api/v1/documents/${doc.id}/reprocess`, { config_id: configID.value }),
    'New revision queued. The previous searchable revision stays available until this one succeeds.')
}

function retryDispatch(doc) {
  return act(() => api.post(`/api/v1/documents/${doc.id}/retry-dispatch`), 'Dispatch accepted.')
}

function remove(doc) {
  deleting.value = null
  if (selected.value?.id === doc.id) selected.value = null
  return act(() => api.delete(`/api/v1/documents/${doc.id}`), 'Document removed from Library and new searches. Historical evidence is retained.')
}

async function inspect(doc) {
  error.value = null
  try { selected.value = await api.get(`/api/v1/documents/${doc.id}`) }
  catch (err) { error.value = err }
}

function ingestionActive(doc) {
  return ['dispatch_pending', 'dispatch_failed', 'queued', 'processing'].includes(doc.job?.state)
}

onMounted(() => refresh(true))
onUnmounted(() => { disposed = true; clearTimeout(timer) })
</script>

<template>
  <section>
    <div class="section-head">
      <div><h2>Document Library</h2><p class="muted">Upload source documents and follow their indexing progress.</p></div>
      <button type="button" :disabled="busy" @click="refresh(true)">Refresh</button>
    </div>
    <form class="upload-panel" @submit.prevent="upload">
      <label>Ingestion configuration
        <select v-model="configID" :disabled="busy || !configs.length" required>
          <option v-for="config in configs" :key="config.id" :value="config.id">
            {{ config.name }} — {{ config.chunk_size }} / {{ config.chunk_overlap }} characters
          </option>
        </select>
      </label>
      <label>Source document (PDF, DOCX or UTF-8 TXT)
        <input ref="fileInput" type="file" accept=".pdf,.docx,.txt" :disabled="busy" required
          @change="file = $event.target.files[0] ?? null" />
      </label>
      <button type="submit" :disabled="busy || !file || !configID">{{ busy ? 'Working…' : 'Upload document' }}</button>
      <p v-if="state !== 'loading' && !configs.length" class="muted">No saved configurations available. See Overview for configuration setup.</p>
      <p class="muted">The selected configuration also applies to reprocessing. Text-bearing files only; OCR is unavailable.</p>
    </form>

    <div v-if="error" class="error" role="alert">
      <strong>{{ error.code }}:</strong> {{ error.message }}
      <button v-if="state === 'error'" type="button" @click="refresh(true)">Retry</button>
    </div>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="state === 'loading'" data-state="loading">Loading documents…</p>
    <p v-else-if="state === 'empty'" data-state="empty">No documents yet. Upload a source document to get started.</p>

    <div v-if="documents.length" class="table-scroll" data-state="ready">
      <table>
        <thead><tr><th>Document</th><th>Status</th><th>Searchable chunks</th><th>Latest job</th><th>Actions</th></tr></thead>
        <tbody>
          <tr v-for="doc in documents" :key="doc.id">
            <td><button class="filename" type="button" @click="inspect(doc)">{{ doc.filename }}</button><small>{{ doc.size_bytes.toLocaleString() }} bytes</small></td>
            <td><span class="status" :data-status="doc.status">{{ doc.status }}</span>
              <small v-if="doc.active_revision_id && doc.active_revision_id !== doc.latest_revision_id">Previous revision remains searchable</small></td>
            <td>{{ doc.chunk_count ?? '—' }}</td>
            <td>{{ doc.job?.stage ?? '—' }} · {{ doc.job?.state ?? '—' }}
              <p v-if="doc.job?.error_code" class="job-error"><strong>{{ doc.job.error_code }}:</strong> {{ doc.job.error_message }}</p>
            </td>
            <td class="actions">
              <button v-if="['dispatch_pending', 'dispatch_failed'].includes(doc.job?.state)" type="button" :disabled="busy" @click="retryDispatch(doc)">Retry dispatch</button>
              <button type="button" :disabled="busy || !configID || ingestionActive(doc)" @click="reprocess(doc)">Reprocess</button>
              <button type="button" :disabled="busy" @click="deleting = doc">Delete</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <section v-if="deleting" class="confirmation" role="alertdialog" aria-labelledby="delete-title">
      <h3 id="delete-title">Delete {{ deleting.filename }}?</h3>
      <p>This removes the document from new searches and cancels processing. Source bytes and historical evidence are retained.</p>
      <button type="button" @click="remove(deleting)">Confirm delete</button>
      <button type="button" @click="deleting = null">Cancel</button>
    </section>

    <section v-if="selected" class="details">
      <div class="section-head"><h3>{{ selected.filename }} — revision history</h3><button type="button" @click="selected = null">Close details</button></div>
      <p><small>Document {{ selected.id }}</small></p>
      <article v-for="revision in selected.revisions" :key="revision.id">
        <h4>Revision {{ revision.revision_number }} · {{ revision.status }} <span v-if="revision.id === selected.active_revision_id">· active</span></h4>
        <p>{{ revision.chunk_size }} / {{ revision.chunk_overlap }} characters · {{ revision.embedding_provider }} / {{ revision.embedding_model }} · {{ revision.embedding_dimensions }} dimensions</p>
        <p v-if="revision.job?.error_message" class="job-error">{{ revision.job.error_code }}: {{ revision.job.error_message }}</p>
        <small>Job {{ revision.job?.id }} · {{ revision.job?.dag_id }} / {{ revision.job?.run_id }}</small>
        <dl><template v-for="(metrics, stage) in revision.job?.stage_metrics" :key="stage"><dt>{{ stage }}</dt><dd>{{ metrics.status }} · {{ metrics.duration_ms }} ms</dd></template></dl>
      </article>
    </section>
  </section>
</template>

<style scoped>
.section-head { display: flex; justify-content: space-between; align-items: center; gap: 1rem; }
h2 { margin-bottom: .35rem; }
.muted, small { color: var(--muted); }
small { display: block; overflow-wrap: anywhere; }
.upload-panel, .details, .confirmation { border: 1px solid var(--border); border-radius: .5rem; padding: 1.25rem; margin: 1rem 0; }
.upload-panel { display: flex; align-items: end; gap: 1rem; flex-wrap: wrap; }
.upload-panel p { margin: 0; flex-basis: 100%; }
label { display: grid; gap: .4rem; }
select, input { font: inherit; max-width: 100%; padding: .4rem; }
.table-scroll { overflow-x: auto; }
table { width: 100%; border-collapse: collapse; }
th, td { text-align: left; padding: .8rem .6rem; border-bottom: 1px solid var(--border); vertical-align: top; }
th { color: var(--muted); font-size: .85rem; }
.filename { padding: 0; border: 0; color: var(--accent); text-align: left; overflow-wrap: anywhere; }
.actions { min-width: 230px; }
.actions button { margin: 0 .3rem .3rem 0; }
.status { display: inline-block; padding: .2rem .5rem; border-radius: 1rem; background: var(--surface-hover); }
[data-status='processed'] { color: var(--ok); }
[data-status='failed'], .job-error { color: var(--danger); }
.job-error { margin: .4rem 0 0; max-width: 30rem; overflow-wrap: anywhere; }
.error { padding: 1rem; border: 1px solid var(--danger); border-radius: .5rem; }
.error button { margin-left: .5rem; }
article { border-top: 1px solid var(--border); }
dl { display: grid; grid-template-columns: 7rem 1fr; font-size: .9rem; }
dd { margin: 0; }
button:disabled { opacity: .5; cursor: not-allowed; }
@media (max-width: 650px) { .upload-panel { display: grid; } }
</style>
