import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import ChatView from './ChatView.vue'
import { api } from '../api/client'

vi.mock('../api/client', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

const config = {
  id: 'cfg-current',
  name: 'Current baseline',
  top_k: 3,
  model_profile: 'openai-gpt-4o-mini',
  embedding_provider: 'openai',
  embedding_model: 'text-embedding-3-small',
}

const success = {
  answer: 'Approval needs two signatures [1].',
  citations: [{ document_id: 'doc-1', chunk_id: 'chunk-1', snippet: 'approval evidence' }],
  trace: {
    trace_id: '11111111-1111-1111-1111-111111111111',
    latency_ms: 42,
    input_tokens: 10,
    output_tokens: 4,
    embedding_input_tokens: 5,
    estimated_cost: 0.000003,
    cost_currency: 'USD',
    cost_unavailable_reason: null,
  },
}

let wrapper
let router

beforeEach(() => {
  vi.clearAllMocks()
  api.get.mockImplementation((path) => {
    if (path === '/api/v1/rag-configs') return Promise.resolve({ configs: [config] })
    return Promise.resolve({})
  })
})

afterEach(() => {
  wrapper?.unmount()
})

async function setup() {
  router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/chat', name: 'chat', component: ChatView }],
  })
  await router.push('/chat')
  await router.isReady()
  wrapper = mount(ChatView, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

async function submit(question = 'How does approval work?') {
  await wrapper.find('textarea').setValue(question)
  await wrapper.find('form').trigger('submit')
  await flushPromises()
}

describe('ChatView', () => {
  it('loads configs, submits the real request, and connects answer to citations and metrics', async () => {
    api.post.mockResolvedValue(success)
    await setup()
    await submit()

    expect(api.post).toHaveBeenCalledWith('/api/v1/chat', {
      question: 'How does approval work?',
      config_id: 'cfg-current',
    })
    expect(wrapper.find('[data-state="answer"]').text()).toContain('Approval needs two signatures [1].')
    expect(wrapper.find('[aria-label="Source citations"]').text()).toContain('approval evidence')
    expect(wrapper.find('[aria-label="Trace summary"]').text()).toContain('42 ms')
    expect(wrapper.find('[aria-label="Trace summary"]').text()).toContain('0.000003 USD')
    expect(wrapper.text()).toContain('Current baseline')
    expect(wrapper.text()).toContain('Open trace 11111111-1111-1111-1111-111111111111')
  })

  it('keeps loading visible while configurations are unavailable', async () => {
    let resolve
    api.get.mockImplementation(() => new Promise((r) => { resolve = r }))
    await routerSetupOnly()
    expect(wrapper.find('[data-state="loading"]').exists()).toBe(true)
    resolve({ configs: [config] })
    await flushPromises()
    expect(wrapper.find('form').exists()).toBe(true)
  })

  it('shows a distinct empty-retrieval state and trace link', async () => {
    api.post.mockRejectedValue({
      code: 'retrieval_empty',
      message: 'No usable evidence',
      trace_id: '22222222-2222-2222-2222-222222222222',
    })
    await setup()
    await submit()

    const state = wrapper.find('[data-state="retrieval-empty"]')
    expect(state.exists()).toBe(true)
    expect(state.text()).toContain('No supporting evidence')
    expect(state.text()).toContain('No usable evidence')
    expect(state.text()).toContain('Open persisted failure trace')
  })

  it('shows provider failures separately from empty retrieval', async () => {
    api.post.mockRejectedValue({ code: 'model_rate_limited', message: 'Provider rate limited' })
    await setup()
    await submit()

    const state = wrapper.find('[data-state="provider-failure"]')
    expect(state.exists()).toBe(true)
    expect(state.text()).toContain('Provider request failed')
    expect(state.text()).toContain('Provider rate limited')
    expect(wrapper.find('[data-state="retrieval-empty"]').exists()).toBe(false)
  })

  it('labels missing cost and preserves token-unavailable values', async () => {
    api.post.mockResolvedValue({
      ...success,
      trace: {
        ...success.trace,
        input_tokens: null,
        output_tokens: null,
        estimated_cost: null,
        cost_currency: null,
        cost_unavailable_reason: 'usage_unavailable',
      },
    })
    await setup()
    await submit()

    expect(wrapper.find('[data-state="cost-unavailable"]').exists()).toBe(true)
    expect(wrapper.find('[data-state="cost-unavailable"]').text()).toContain('Unavailable')
    expect(wrapper.find('[data-state="cost-unavailable"]').text()).toContain('usage_unavailable')
    expect(wrapper.find('[aria-label="Trace summary"]').text()).toContain('Input tokensUnavailable')
  })

  it('renders citation text as text, never HTML', async () => {
    api.post.mockResolvedValue({
      ...success,
      citations: [{ document_id: 'doc-1', chunk_id: 'chunk-html', snippet: '<img src=x onerror=alert(1)> evidence' }],
    })
    await setup()
    await submit()

    expect(wrapper.find('[aria-label="Source citations"]').text()).toContain('<img src=x onerror=alert(1)> evidence')
    expect(wrapper.find('[aria-label="Source citations"] img').exists()).toBe(false)
    expect(wrapper.html()).not.toContain('v-html')
  })

  it('reopens a trace from the persisted detail contract, not current config state', async () => {
    const traceID = success.trace.trace_id
    api.post.mockResolvedValue(success)
    api.get.mockImplementation((path) => {
      if (path === '/api/v1/rag-configs') return Promise.resolve({ configs: [config] })
      if (path === `/api/v1/traces/${traceID}`) {
        return Promise.resolve({
          trace_id: traceID,
          request_type: 'chat',
          success: true,
          rag_config_id: 'cfg-historical',
          config: { id: 'cfg-historical', name: 'Historical baseline', embedding_model: 'old-model' },
          prompt_snapshot: { prompt_version: 'v1' },
          context_snapshot: [{ rank: 1, document_id: 'doc-old', chunk_id: 'chunk-old', revision_id: 'rev-old', content: 'historical evidence' }],
          spans: [{ span_name: 'retrieval', started_at: '2026-09-16T12:00:00Z', duration_ms: 3 }],
        })
      }
      return Promise.resolve({})
    })
    await setup()
    await submit()

    await router.push({ name: 'chat', query: { trace: traceID } })
    await flushPromises()
    expect(api.get).toHaveBeenCalledWith(`/api/v1/traces/${traceID}`)
    expect(wrapper.find('[data-state="trace-ready"]').text()).toContain('Historical baseline')
    expect(wrapper.find('[data-state="trace-ready"]').text()).toContain('historical evidence')
    expect(wrapper.find('[data-state="trace-ready"]').text()).not.toContain('Current baseline')
  })
  it('ignores a stale trace response after navigating to another trace', async () => {
    let resolveA
    let resolveB
    api.get.mockImplementation((path) => {
      if (path === '/api/v1/rag-configs') return Promise.resolve({ configs: [config] })
      if (path === '/api/v1/traces/trace-a') return new Promise((resolve) => { resolveA = resolve })
      if (path === '/api/v1/traces/trace-b') return new Promise((resolve) => { resolveB = resolve })
      return Promise.resolve({})
    })

    router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/chat', name: 'chat', component: ChatView }],
    })
    await router.push({ name: 'chat', query: { trace: 'trace-a' } })
    await router.isReady()
    wrapper = mount(ChatView, { global: { plugins: [router] } })
    await flushPromises()
    await router.push({ name: 'chat', query: { trace: 'trace-b' } })
    await flushPromises()

    resolveA({ trace_id: 'trace-a', success: true, config: { name: 'Old trace' }, context_snapshot: [], spans: [] })
    await flushPromises()
    expect(wrapper.find('[data-state="trace-loading"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('Old trace')

    resolveB({ trace_id: 'trace-b', success: true, config: { name: 'New trace' }, context_snapshot: [], spans: [] })
    await flushPromises()
    expect(wrapper.find('[data-state="trace-ready"]').text()).toContain('New trace')
    expect(wrapper.find('[data-state="trace-ready"]').text()).not.toContain('Old trace')
  })

})

async function routerSetupOnly() {
  router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/chat', name: 'chat', component: ChatView }],
  })
  await router.push('/chat')
  await router.isReady()
  wrapper = mount(ChatView, { global: { plugins: [router] } })
}
