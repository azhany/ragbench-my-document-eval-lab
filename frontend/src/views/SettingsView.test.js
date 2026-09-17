import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import SettingsView from './SettingsView.vue'
import { api } from '../api/client'

vi.mock('../api/client', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

const catalog = {
  providers: [
    { id: 'opencode-go', label: 'OpenCode Go', protocol: 'OpenAI-compatible', roles: ['generation'], credential_note: 'Server environment' },
    { id: 'openai', label: 'OpenAI', protocol: 'OpenAI-compatible', roles: ['generation', 'embedding'], credential_note: 'Server environment' },
  ],
  model_profiles: [
    { id: 'gen-1', name: 'support-model-v1', kind: 'generation', provider: 'opencode-go', model: 'glm-5.3-flash', dimensions: null, enabled: true },
    { id: 'emb-1', name: 'support-embedding-v1', kind: 'embedding', provider: 'openai', model: 'text-embedding-3-small', dimensions: 1536, enabled: true },
  ],
  prompt_versions: ['v1'],
  reranker_profiles: ['lexical-v1'],
  limits: {},
}

let wrapper
let router

beforeEach(() => {
  vi.clearAllMocks()
  api.get.mockImplementation((path) => {
    if (path === '/api/v1/settings') return Promise.resolve(structuredClone(catalog))
    if (path === '/api/v1/rag-configs') return Promise.resolve({ configs: [] })
    return Promise.resolve({})
  })
  router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/settings', name: 'settings', component: SettingsView }],
  })
})

afterEach(() => wrapper?.unmount())

async function setup() {
  await router.push('/settings')
  await router.isReady()
  wrapper = mount(SettingsView, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('SettingsView', () => {
  it('loads persisted profiles and exposes them in the RAG builder', async () => {
    await setup()

    expect(wrapper.find('[data-state="ready"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="model-profile"]').text()).toContain('support-model-v1')
    expect(wrapper.find('[data-test="embedding-profile"]').text()).toContain('support-embedding-v1')
    expect(wrapper.text()).toContain('Credentials stay server-side')
  })

  it('saves the complete RAG configuration through the API', async () => {
    api.post.mockResolvedValue({ id: 'cfg-1', name: 'support-search-v2' })
    await setup()

    await wrapper.find('[data-test="config-name"]').setValue('support-search-v2')
    await wrapper.find('[data-test="retrieval-mode"]').setValue('hybrid')
    await wrapper.find('[data-test="config-editor"]').trigger('submit')
    await flushPromises()

    expect(api.post).toHaveBeenCalledWith('/api/v1/rag-configs', expect.objectContaining({
      name: 'support-search-v2',
      retrieval_mode: 'hybrid',
      model_profile: 'support-model-v1',
      embedding_profile: 'support-embedding-v1',
      fusion_method: 'rrf',
      rrf_rank_constant: 60,
    }))
    expect(wrapper.text()).toContain('Saved support-search-v2')
  })

  it('adds a model profile without accepting credentials in the payload', async () => {
    api.post.mockResolvedValue({
      id: 'gen-2', name: 'local-model-v1', kind: 'generation', provider: 'opencode-go',
      model: 'new-model', dimensions: null, enabled: true,
    })
    await setup()
    await wrapper.findAll('button').find((button) => button.text().includes('Models & providers')).trigger('click')
    await wrapper.find('[data-test="profile-name"]').setValue('local-model-v1')
    await wrapper.find('[data-test="profile-model"]').setValue('new-model')
    await wrapper.find('[data-test="profile-editor"]').trigger('submit')
    await flushPromises()

    expect(api.post).toHaveBeenCalledWith('/api/v1/settings/model-profiles', {
      name: 'local-model-v1', kind: 'generation', provider: 'opencode-go', model: 'new-model', dimensions: 0,
    })
    expect(wrapper.text()).toContain('local-model-v1')
    expect(wrapper.text()).not.toContain('api_key')
  })

  it('shows an actionable error when the settings API is unavailable', async () => {
    api.get.mockRejectedValue({ message: 'Cannot reach API' })
    await setup()

    expect(wrapper.find('[data-state="error"]').exists()).toBe(true)
    expect(wrapper.find('[role="alert"]').text()).toContain('Cannot reach API')
    expect(wrapper.text()).toContain('Retry')
  })
})
