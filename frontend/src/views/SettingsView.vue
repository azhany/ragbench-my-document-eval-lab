<script setup>
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'

const route = useRoute()
const router = useRouter()

const state = ref('loading')
const error = ref(null)
const settings = ref(null)
const configs = ref([])
const notice = ref('')
const configBusy = ref(false)
const profileBusy = ref(false)
const configError = ref(null)
const profileError = ref(null)
const activeTab = ref(route.query.tab === 'models' ? 'models' : 'rag')

const configForm = reactive({
  name: '',
  chunk_size: 500,
  chunk_overlap: 80,
  retrieval_mode: 'vector',
  top_k: 5,
  rerank_enabled: false,
  reranker_profile: 'lexical-v1',
  rerank_candidate_limit: 20,
  fusion_method: 'rrf',
  rrf_rank_constant: 60,
  fts_candidate_limit: 20,
  vector_candidate_limit: 20,
  prompt_version: 'v1',
  model_profile: '',
  embedding_profile: '',
})

const profileForm = reactive({
  name: '',
  kind: 'generation',
  provider: 'opencode-go',
  model: '',
  dimensions: 1536,
})

const modelProfiles = computed(() => settings.value?.model_profiles ?? [])
const generationProfiles = computed(() => modelProfiles.value.filter((profile) => profile.kind === 'generation' && profile.enabled))
const embeddingProfiles = computed(() => modelProfiles.value.filter((profile) => profile.kind === 'embedding' && profile.enabled))
const providers = computed(() => settings.value?.providers ?? [])
const promptVersions = computed(() => settings.value?.prompt_versions?.length ? settings.value.prompt_versions : ['v1'])
const rerankerProfiles = computed(() => settings.value?.reranker_profiles?.length ? settings.value.reranker_profiles : ['lexical-v1'])
const limits = computed(() => settings.value?.limits ?? {})
const availableProviders = computed(() => providers.value.filter((provider) => provider.roles?.includes(profileForm.kind)))

function setProfileDefaults() {
  if (!configForm.model_profile && generationProfiles.value.length) configForm.model_profile = generationProfiles.value[0].name
  if (!configForm.embedding_profile && embeddingProfiles.value.length) configForm.embedding_profile = embeddingProfiles.value[0].name
  if (!promptVersions.value.includes(configForm.prompt_version)) configForm.prompt_version = promptVersions.value[0]
  if (!rerankerProfiles.value.includes(configForm.reranker_profile)) configForm.reranker_profile = rerankerProfiles.value[0]
  if (!availableProviders.value.some((provider) => provider.id === profileForm.provider)) {
    profileForm.provider = availableProviders.value[0]?.id ?? ''
  }
}

async function load() {
  state.value = 'loading'
  error.value = null
  try {
    const [catalog, configList] = await Promise.all([
      api.get('/api/v1/settings'),
      api.get('/api/v1/rag-configs'),
    ])
    settings.value = catalog
    configs.value = configList.configs ?? []
    setProfileDefaults()
    state.value = 'ready'
  } catch (err) {
    error.value = err
    state.value = 'error'
  }
}

function errorMessage(err) {
  if (!err) return ''
  const fields = Array.isArray(err.fields) ? err.fields : []
  if (fields.length) return `${err.message} ${fields.map((field) => `${field.field}: ${field.message}`).join('; ')}`
  return err.message ?? 'Unknown settings error'
}

function providerLabel(id) {
  return providers.value.find((provider) => provider.id === id)?.label ?? id
}

function profileDescription(profile) {
  const dimensions = profile.dimensions ? ` · ${profile.dimensions}d` : ''
  return `${providerLabel(profile.provider)} · ${profile.model}${dimensions}`
}

function profileCount(kind) {
  return modelProfiles.value.filter((profile) => profile.kind === kind).length
}

async function saveConfig() {
  configError.value = null
  notice.value = ''
  configBusy.value = true
  try {
    const created = await api.post('/api/v1/rag-configs', {
      ...configForm,
      chunk_size: Number(configForm.chunk_size),
      chunk_overlap: Number(configForm.chunk_overlap),
      top_k: Number(configForm.top_k),
      rerank_candidate_limit: Number(configForm.rerank_candidate_limit),
      rrf_rank_constant: Number(configForm.rrf_rank_constant),
      fts_candidate_limit: Number(configForm.fts_candidate_limit),
      vector_candidate_limit: Number(configForm.vector_candidate_limit),
    })
    configs.value = [...configs.value, created].sort((a, b) => a.name.localeCompare(b.name))
    configForm.name = ''
    notice.value = `Saved ${created.name} as an immutable configuration.`
  } catch (err) {
    configError.value = err
  } finally {
    configBusy.value = false
  }
}

async function addProfile() {
  profileError.value = null
  notice.value = ''
  profileBusy.value = true
  try {
    const created = await api.post('/api/v1/settings/model-profiles', {
      name: profileForm.name,
      kind: profileForm.kind,
      provider: profileForm.provider,
      model: profileForm.model,
      dimensions: profileForm.kind === 'embedding' ? Number(profileForm.dimensions) : 0,
    })
    settings.value.model_profiles = [...modelProfiles.value, created].sort((a, b) => {
      if (a.kind !== b.kind) return a.kind.localeCompare(b.kind)
      return a.name.localeCompare(b.name)
    })
    if (created.kind === 'generation' && !configForm.model_profile) configForm.model_profile = created.name
    if (created.kind === 'embedding' && !configForm.embedding_profile) configForm.embedding_profile = created.name
    profileForm.name = ''
    profileForm.model = ''
    notice.value = `Added ${created.name} to the ${created.kind} profile catalog.`
  } catch (err) {
    profileError.value = err
  } finally {
    profileBusy.value = false
  }
}

async function selectTab(tab) {
  activeTab.value = tab
  await router.replace({ query: tab === 'models' ? { tab: 'models' } : {} })
}

watch(() => route.query.tab, (tab) => {
  if (tab === 'models' || tab === 'rag') activeTab.value = tab
})
watch(() => profileForm.kind, () => {
  profileForm.provider = availableProviders.value[0]?.id ?? ''
})

onMounted(load)
</script>

<template>
  <section class="view settings-view" :data-state="state">
    <header class="page-heading">
      <div>
        <p class="eyebrow">Workspace / Infrastructure</p>
        <h2>Settings</h2>
        <p>Manage the provider profiles and immutable RAG configurations used by chat, indexing, and evaluation runs.</p>
      </div>
      <span class="server-note"><span class="online-dot"></span> Credentials stay server-side</span>
    </header>

    <p v-if="state === 'loading'" data-state="loading">Loading settings catalog…</p>
    <section v-else-if="state === 'error'" class="panel error" data-state="error" role="alert">
      <strong>Settings unavailable.</strong> {{ errorMessage(error) }}
      <button type="button" @click="load">Retry</button>
    </section>

    <template v-else>
      <div v-if="notice" class="notice" role="status">{{ notice }}</div>

      <div class="settings-tabs" role="tablist" aria-label="Settings sections">
        <button type="button" :class="{ active: activeTab === 'rag' }" :aria-selected="activeTab === 'rag'" role="tab" @click="selectTab('rag')">
          RAG configurations <span>{{ configs.length }}</span>
        </button>
        <button type="button" :class="{ active: activeTab === 'models' }" :aria-selected="activeTab === 'models'" role="tab" @click="selectTab('models')">
          Models &amp; providers <span>{{ modelProfiles.length }}</span>
        </button>
      </div>

      <section v-if="activeTab === 'rag'" class="settings-layout" role="tabpanel">
        <form class="panel config-editor" data-test="config-editor" @submit.prevent="saveConfig">
          <div class="panel-heading">
            <div><span class="section-kicker">Configuration builder</span><h3>Create a RAG configuration</h3></div>
            <span class="immutable-badge">Immutable identity</span>
          </div>
          <p class="helper">Changing settings creates a new saved identity. Existing traces and evaluations never change.</p>

          <fieldset>
            <legend>Identity</legend>
            <label class="full-field">Configuration name
              <input v-model="configForm.name" required maxlength="200" placeholder="e.g. support-search-v2" data-test="config-name" />
            </label>
          </fieldset>

          <fieldset>
            <legend>Retrieval</legend>
            <div class="form-grid three-columns">
              <label>Chunk size
                <input v-model.number="configForm.chunk_size" type="number" :min="limits.min_chunk_size || 1" :max="limits.max_chunk_size || 8192" required data-test="chunk-size" />
              </label>
              <label>Chunk overlap
                <input v-model.number="configForm.chunk_overlap" type="number" min="0" :max="Math.max(0, Number(configForm.chunk_size) - 1)" required data-test="chunk-overlap" />
              </label>
              <label>Top-k
                <input v-model.number="configForm.top_k" type="number" :min="limits.min_top_k || 1" :max="limits.max_top_k || 100" required data-test="top-k" />
              </label>
              <label>Retrieval mode
                <select v-model="configForm.retrieval_mode" data-test="retrieval-mode"><option value="vector">Vector</option><option value="hybrid">Hybrid + RRF</option></select>
              </label>
              <label>RRF rank constant
                <input v-model.number="configForm.rrf_rank_constant" type="number" :min="limits.min_rrf_rank_constant || 1" :max="limits.max_rrf_rank_constant || 1000" required data-test="rrf-constant" />
              </label>
              <label>Branch candidates
                <input v-model.number="configForm.vector_candidate_limit" type="number" :min="limits.min_candidate_limit || 1" :max="limits.max_candidate_limit || 100" required data-test="vector-candidates" />
              </label>
            </div>
            <label class="toggle-field"><input v-model="configForm.rerank_enabled" type="checkbox" /> Enable lexical reranking <span>local · no provider spend</span></label>
            <div v-if="configForm.rerank_enabled" class="form-grid two-columns nested-fields">
              <label>Reranker profile
                <select v-model="configForm.reranker_profile"><option v-for="profile in rerankerProfiles" :key="profile" :value="profile">{{ profile }}</option></select>
              </label>
              <label>Rerank candidates
                <input v-model.number="configForm.rerank_candidate_limit" type="number" :min="limits.min_candidate_limit || 1" :max="limits.max_candidate_limit || 100" />
              </label>
            </div>
          </fieldset>

          <fieldset>
            <legend>Models &amp; prompt</legend>
            <div class="form-grid two-columns">
              <label>Generation model
                <select v-model="configForm.model_profile" required data-test="model-profile" :disabled="!generationProfiles.length">
                  <option v-for="profile in generationProfiles" :key="profile.id" :value="profile.name">{{ profile.name }} — {{ profile.model }}</option>
                </select>
              </label>
              <label>Embedding model
                <select v-model="configForm.embedding_profile" required data-test="embedding-profile" :disabled="!embeddingProfiles.length">
                  <option v-for="profile in embeddingProfiles" :key="profile.id" :value="profile.name">{{ profile.name }} — {{ profile.dimensions }}d</option>
                </select>
              </label>
              <label>Prompt version
                <select v-model="configForm.prompt_version"><option v-for="version in promptVersions" :key="version" :value="version">{{ version }}</option></select>
              </label>
              <label>FTS candidates
                <input v-model.number="configForm.fts_candidate_limit" type="number" :min="limits.min_candidate_limit || 1" :max="limits.max_candidate_limit || 100" required />
              </label>
            </div>
            <p v-if="!generationProfiles.length || !embeddingProfiles.length" class="inline-warning">Add an enabled generation and embedding profile in Models &amp; providers before saving.</p>
          </fieldset>

          <p v-if="configError" class="form-error" role="alert">{{ errorMessage(configError) }}</p>
          <div class="form-actions"><button class="primary-button" type="submit" :disabled="configBusy || !generationProfiles.length || !embeddingProfiles.length">{{ configBusy ? 'Saving…' : 'Save configuration' }}</button><span class="helper">Saved to PostgreSQL</span></div>
        </form>

        <aside class="settings-side">
          <section class="panel config-preview">
            <div class="panel-heading"><div><span class="section-kicker">Live preview</span><h3>Run identity</h3></div><span class="preview-mark">●</span></div>
            <p class="helper">This is the exact identity a new run will pin.</p>
            <dl>
              <div><dt>Generation</dt><dd>{{ configForm.model_profile || 'Select a model' }}</dd></div>
              <div><dt>Embedding</dt><dd>{{ configForm.embedding_profile || 'Select an embedding' }}</dd></div>
              <div><dt>Retrieval</dt><dd>{{ configForm.retrieval_mode }} · top-k {{ configForm.top_k }}</dd></div>
              <div><dt>Chunking</dt><dd>{{ configForm.chunk_size }} / {{ configForm.chunk_overlap }} chars</dd></div>
              <div><dt>Reranking</dt><dd>{{ configForm.rerank_enabled ? configForm.reranker_profile : 'Disabled' }}</dd></div>
            </dl>
          </section>
          <section class="panel settings-tip">
            <span class="section-kicker">Why this matters</span>
            <h3>Reproducible by default</h3>
            <p>Provider names and model IDs are stored with each configuration and trace. API keys never enter the browser or database.</p>
          </section>
        </aside>
      </section>

      <section v-else class="models-settings" role="tabpanel">
        <div class="providers-grid">
          <article v-for="provider in providers" :key="provider.id" class="provider-card">
            <div class="provider-card-head"><span class="provider-mark">{{ provider.label.slice(0, 1) }}</span><span class="provider-state"><span class="online-dot"></span> Adapter ready</span></div>
            <h3>{{ provider.label }}</h3>
            <p>{{ provider.protocol }}</p>
            <div class="provider-roles"><span v-for="role in provider.roles" :key="role">{{ role }}</span></div>
            <small>{{ provider.credential_note }} · endpoint is server-managed</small>
          </article>
        </div>

        <div class="models-layout">
          <form class="panel profile-editor" data-test="profile-editor" @submit.prevent="addProfile">
            <div class="panel-heading"><div><span class="section-kicker">Catalog entry</span><h3>Add a model profile</h3></div><span class="preview-mark">＋</span></div>
            <p class="helper">Register a model ID behind an existing provider adapter. No key or secret is accepted here.</p>
            <label>Profile name
              <input v-model="profileForm.name" pattern="[a-z0-9][a-z0-9._-]*" placeholder="my-model-v1" required data-test="profile-name" />
            </label>
            <label>Profile type
              <select v-model="profileForm.kind" data-test="profile-kind"><option value="generation">Generation</option><option value="embedding">Embedding</option></select>
            </label>
            <label>Provider adapter
              <select v-model="profileForm.provider" required data-test="profile-provider"><option v-for="provider in availableProviders" :key="provider.id" :value="provider.id">{{ provider.label }} · {{ provider.protocol }}</option></select>
            </label>
            <label>Provider model ID
              <input v-model="profileForm.model" placeholder="model-name-from-provider" required data-test="profile-model" />
            </label>
            <label v-if="profileForm.kind === 'embedding'">Vector dimensions
              <input v-model.number="profileForm.dimensions" type="number" min="1" required data-test="profile-dimensions" />
            </label>
            <p v-if="profileError" class="form-error" role="alert">{{ errorMessage(profileError) }}</p>
            <button class="primary-button" type="submit" :disabled="profileBusy || !availableProviders.length">{{ profileBusy ? 'Adding…' : 'Add profile' }}</button>
          </form>

          <section class="panel profile-list">
            <div class="panel-heading"><div><span class="section-kicker">Persisted catalog</span><h3>Model profiles</h3></div><span class="helper">{{ profileCount('generation') }} gen · {{ profileCount('embedding') }} emb</span></div>
            <p class="helper">These names are selectable in the RAG builder. Credentials and endpoint values are never shown here.</p>
            <div class="table-scroll">
              <table data-test="profile-list">
                <thead><tr><th>Profile</th><th>Role</th><th>Provider</th><th>Model ID</th><th>Shape</th></tr></thead>
                <tbody>
                  <tr v-for="profile in modelProfiles" :key="profile.id">
                    <td><strong>{{ profile.name }}</strong><span class="table-subtext">{{ profile.enabled ? 'Enabled' : 'Disabled' }}</span></td>
                    <td><span class="role-badge" :data-kind="profile.kind">{{ profile.kind }}</span></td>
                    <td>{{ providerLabel(profile.provider) }}</td>
                    <td><code>{{ profile.model }}</code></td>
                    <td>{{ profile.dimensions || 'Chat completion' }}<span v-if="profile.dimensions"> dimensions</span></td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
        </div>
      </section>
    </template>
  </section>
</template>

<style scoped>
.settings-view { gap: 20px; }
.eyebrow, .section-kicker { margin: 0 0 5px; color: var(--accent); font-size: 10px; font-weight: 800; letter-spacing: .12em; text-transform: uppercase; }
.server-note { display: inline-flex; align-items: center; gap: 8px; align-self: center; padding: 7px 10px; border: 1px solid #cfe9dd; border-radius: 999px; color: var(--green); background: var(--green-soft); font-size: 11px; font-weight: 700; white-space: nowrap; }
.server-note .online-dot { box-shadow: none; }
.notice { padding: 10px 14px; border: 1px solid #cfe9dd; border-radius: 8px; color: var(--green); background: var(--green-soft); }
.settings-tabs { display: flex; gap: 4px; padding: 4px; border: 1px solid var(--border); border-radius: 9px; background: #edf2f9; }
.settings-tabs button { min-height: 36px; border-color: transparent; background: transparent; color: var(--muted); font-weight: 700; }
.settings-tabs button.active { border-color: var(--border); color: var(--text); background: var(--surface); box-shadow: 0 2px 5px rgba(20, 42, 80, .05); }
.settings-tabs button span { margin-left: 5px; padding: 2px 6px; border-radius: 999px; color: var(--muted); background: var(--code-bg); font-size: 10px; }
.settings-layout { display: grid; grid-template-columns: minmax(0, 1.65fr) minmax(260px, .8fr); gap: 16px; align-items: start; }
.settings-side { display: grid; gap: 16px; }
.panel-heading { display: flex; align-items: start; justify-content: space-between; gap: 15px; }
.panel-heading h3 { margin: 0; font-size: 16px; }
.helper { color: var(--muted); font-size: 11px; }
.config-editor, .profile-editor, .profile-list { padding: 20px; }
fieldset { margin: 22px 0 0; padding: 16px 0 0; border: 0; border-top: 1px solid var(--border); }
legend { padding: 0 9px 0 0; color: var(--muted-strong); font-size: 11px; font-weight: 800; letter-spacing: .08em; text-transform: uppercase; }
label { display: grid; gap: 6px; color: var(--muted-strong); font-size: 11px; font-weight: 700; }
.full-field { max-width: 430px; }
.form-grid { display: grid; gap: 13px; }
.two-columns { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.three-columns { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.nested-fields { margin-top: 13px; padding-top: 13px; border-top: 1px dashed var(--border); }
.toggle-field { display: flex; grid-template-columns: none; flex-wrap: wrap; align-items: center; gap: 8px; margin-top: 15px; }
.toggle-field input { min-height: auto; width: 15px; height: 15px; accent-color: var(--accent); }
.toggle-field span { color: var(--muted); font-weight: 500; }
.inline-warning { margin: 14px 0 0; padding: 9px 11px; border-radius: 7px; color: var(--warn); background: var(--warn-bg); font-size: 11px; }
.form-actions { display: flex; align-items: center; gap: 12px; margin-top: 23px; }
.form-actions .helper { margin: 0; }
.immutable-badge, .role-badge { padding: 4px 8px; border-radius: 999px; color: var(--purple); background: var(--purple-soft); font-size: 10px; font-weight: 800; }
.config-preview { padding: 20px; }
.preview-mark { color: var(--accent); font-size: 18px; font-weight: 800; }
.config-preview dl { display: grid; gap: 14px; margin-top: 20px; }
.config-preview dl div { padding-bottom: 12px; border-bottom: 1px solid var(--border); }
.config-preview dd { overflow-wrap: anywhere; font-size: 12px; }
.settings-tip { padding: 18px; background: var(--accent-soft); }
.settings-tip h3 { margin: 0 0 8px; }
.settings-tip p { margin: 0; color: var(--muted-strong); font-size: 11px; }
.models-settings { display: grid; gap: 16px; }
.providers-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr)); gap: 12px; }
.provider-card { padding: 16px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface); }
.provider-card-head { display: flex; align-items: center; justify-content: space-between; gap: 7px; }
.provider-mark { display: grid; width: 28px; height: 28px; place-items: center; border-radius: 8px; color: #fff; background: var(--purple); font-weight: 800; }
.provider-state { display: inline-flex; align-items: center; gap: 5px; color: var(--green); font-size: 9px; font-weight: 700; }
.provider-state .online-dot { width: 5px; height: 5px; box-shadow: none; }
.provider-card h3 { margin: 15px 0 3px; font-size: 13px; }
.provider-card p { margin: 0 0 11px; color: var(--muted); font-size: 10px; }
.provider-card small { display: block; margin-top: 12px; color: var(--muted); font-size: 9px; }
.provider-roles { display: flex; gap: 5px; }
.provider-roles span { padding: 3px 6px; border-radius: 4px; color: var(--accent-dark); background: var(--accent-soft); font-size: 9px; font-weight: 700; }
.models-layout { display: grid; grid-template-columns: minmax(230px, .65fr) minmax(0, 1.7fr); gap: 16px; align-items: start; }
.profile-editor { display: grid; gap: 14px; }
.profile-editor .panel-heading { margin-bottom: -2px; }
.profile-editor .helper { margin: -4px 0 3px; }
.profile-list { min-width: 0; }
.profile-list .table-scroll { margin-top: 15px; }
.profile-list table { min-width: 650px; }
.profile-list th, .profile-list td { padding: 11px 9px; }
.table-subtext { display: block; margin-top: 3px; color: var(--muted); font-size: 10px; }
.role-badge[data-kind='embedding'] { color: var(--green); background: var(--green-soft); }
.form-error { margin: 0; font-size: 11px; }
.error button { margin-left: 10px; }
@media (max-width: 980px) {
  .settings-layout, .models-layout { grid-template-columns: 1fr; }
  .settings-side { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 650px) {
  .server-note { align-self: start; white-space: normal; }
  .two-columns, .three-columns, .settings-side { grid-template-columns: 1fr; }
  .settings-tabs { display: grid; }
}
</style>
