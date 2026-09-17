<script setup>
import { onMounted, ref } from 'vue'
import { api } from '../api/client'

// State machine: loading → ready | empty | error. Each state is visually and
// semantically distinct; the error state is actionable (cause + retry).
const configs = ref([])
const state = ref('loading')
const error = ref(null)

async function refresh() {
  state.value = 'loading'
  error.value = null
  try {
    const payload = await api.get('/api/v1/rag-configs')
    configs.value = payload?.configs ?? []
    state.value = configs.value.length > 0 ? 'ready' : 'empty'
  } catch (err) {
    error.value = err
    state.value = 'error'
  }
}

onMounted(refresh)

function formatDateTime(value) {
  if (!value) return '—'
  return new Date(value).toLocaleString()
}
</script>

<template>
  <section class="config-list">
    <div class="section-head">
      <h2>Saved RAG configurations</h2>
      <button v-if="state === 'error' || state === 'ready'" type="button" @click="refresh">
        Refresh
      </button>
    </div>

    <p v-if="state === 'loading'" class="status" data-state="loading">
      Loading configurations…
    </p>

    <div v-else-if="state === 'error'" class="status error" data-state="error" role="alert">
      <p><strong>Could not load configurations.</strong> {{ error.message }}</p>
      <p v-if="error.kind === 'http' && error.status" class="detail">
        The API answered with HTTP {{ error.status }} (code: <code>{{ error.code }}</code>).
      </p>
      <button type="button" @click="refresh">Retry</button>
    </div>

    <p v-else-if="state === 'empty'" class="status" data-state="empty">
      No configurations saved yet. Open <code>Settings</code> to create one
      through the UI, or use <code>POST /api/v1/rag-configs</code> for the
      documented API contract.
    </p>

    <table v-else data-state="ready">
      <thead>
        <tr>
          <th>Name</th>
          <th>Chunk size / overlap</th>
          <th>Retrieval</th>
          <th>Top-k</th>
          <th>Prompt</th>
          <th>Generation model</th>
          <th>Embedding</th>
          <th>Created</th>
          <th>Notes</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="cfg in configs" :key="cfg.id">
          <td class="name">{{ cfg.name }}</td>
          <td>{{ cfg.chunk_size }} / {{ cfg.chunk_overlap }}</td>
          <td>
            {{ cfg.retrieval_mode }}
            <span v-if="cfg.rerank_enabled" class="badge">{{ cfg.reranker_profile || 'rerank' }} · {{ cfg.rerank_candidate_limit || '—' }} candidates</span>
          </td>
          <td>{{ cfg.top_k }}</td>
          <td>{{ cfg.prompt_version }}</td>
          <td><code>{{ cfg.model_profile }}</code><span v-if="cfg.model_provider" class="dim"> · {{ cfg.model_provider }}/{{ cfg.model_name }}</span></td>
          <td>
            <code>{{ cfg.embedding_profile }}</code>
            <span class="dim"> ({{ cfg.embedding_dimensions }}d)</span>
          </td>
          <td class="dim">{{ formatDateTime(cfg.created_at) }}</td>
          <td>
            <span
              v-for="cap in cfg.unavailable_capabilities"
              :key="cap"
              class="badge unavailable"
              :title="'Execution is rejected because this capability is unavailable in the running binary'"
            >
              {{ cap }} unavailable
            </span>
            <span v-if="!cfg.unavailable_capabilities?.length" class="dim">—</span>
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<style scoped>
.config-list { display: grid; gap: 12px; }
h2 { margin: 0; font-size: 16px; }
.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}
.status {
  border: 1px solid var(--border);
  border-radius: 0.5rem;
  padding: 1rem 1.25rem;
  background: var(--surface);
}
.status.error {
  border-color: var(--danger);
}
.status.error strong {
  color: var(--danger);
}
.detail {
  color: var(--muted);
  font-size: 0.9rem;
}
table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.9rem;
}
th,
td {
  text-align: left;
  padding: 0.5rem 0.6rem;
  border-bottom: 1px solid var(--border);
  vertical-align: top;
}
th {
  color: var(--muted);
  font-weight: 600;
  font-size: 0.8rem;
  text-transform: uppercase;
  letter-spacing: 0.03em;
}
.name {
  font-weight: 600;
}
code {
  background: var(--code-bg);
  padding: 0.1rem 0.3rem;
  border-radius: 0.3rem;
  font-size: 0.8rem;
}
.dim {
  color: var(--muted);
}
.badge {
  display: inline-block;
  margin-left: 0.35rem;
  padding: 0.05rem 0.45rem;
  border-radius: 999px;
  font-size: 0.72rem;
  font-weight: 600;
  background: var(--surface-hover);
  color: var(--muted);
}
.badge.unavailable {
  background: var(--warn-bg);
  color: var(--warn);
}
</style>
