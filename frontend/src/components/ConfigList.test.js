import { describe, expect, it, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

vi.mock('../api/client', () => ({
  api: { get: vi.fn() },
}))

import { api } from '../api/client'
import ConfigList from './ConfigList.vue'

const sampleConfig = {
  id: '0b6c8cb1-6a7d-4d3f-9f6a-52f0a1b2c3d4',
  name: 'baseline-v1',
  chunk_size: 500,
  chunk_overlap: 80,
  retrieval_mode: 'vector',
  top_k: 5,
  rerank_enabled: false,
  prompt_version: 'v1',
  model_profile: 'openai-gpt-4o-mini',
  embedding_profile: 'openai-text-embedding-3-small',
  embedding_provider: 'openai',
  embedding_model: 'text-embedding-3-small',
  embedding_dimensions: 1536,
  created_at: '2026-09-14T10:00:00Z',
  unavailable_capabilities: [],
}

function lastState(wrapper) {
  return wrapper.find('[data-state]').attributes('data-state')
}

describe('ConfigList', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows the loading state before the request resolves', async () => {
    let resolveFetch
    api.get.mockReturnValue(
      new Promise((resolve) => {
        resolveFetch = resolve
      }),
    )
    const wrapper = mount(ConfigList)
    expect(lastState(wrapper)).toBe('loading')
    resolveFetch({ configs: [sampleConfig] })
    await flushPromises()
    expect(lastState(wrapper)).toBe('ready')
  })

  it('renders saved configurations from the backend', async () => {
    api.get.mockResolvedValue({ configs: [sampleConfig] })
    const wrapper = mount(ConfigList)
    await flushPromises()

    expect(lastState(wrapper)).toBe('ready')
    expect(wrapper.text()).toContain('baseline-v1')
    expect(wrapper.text()).toContain('openai-gpt-4o-mini')
    expect(wrapper.text()).toContain('1536')
  })

  it('shows a distinct empty state with an actionable hint', async () => {
    api.get.mockResolvedValue({ configs: [] })
    const wrapper = mount(ConfigList)
    await flushPromises()

    expect(lastState(wrapper)).toBe('empty')
    expect(wrapper.text()).toContain('No configurations saved yet')
    expect(wrapper.text()).toContain('POST /api/v1/rag-configs')
  })

  it('shows an actionable error state on backend failure with a retry', async () => {
    api.get.mockRejectedValue({
      kind: 'unreachable',
      code: 'unreachable',
      message: 'Cannot reach the API. Check that the backend container is running.',
    })
    const wrapper = mount(ConfigList)
    await flushPromises()

    expect(lastState(wrapper)).toBe('error')
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Cannot reach the API')
    expect(wrapper.text()).toContain('Retry')
  })

  it('flags unavailable capabilities explicitly', async () => {
    api.get.mockResolvedValue({
      configs: [
        {
          ...sampleConfig,
          retrieval_mode: 'hybrid',
          rerank_enabled: true,
          unavailable_capabilities: ['hybrid_retrieval', 'rerank'],
        },
      ],
    })
    const wrapper = mount(ConfigList)
    await flushPromises()

    expect(wrapper.text()).toContain('hybrid_retrieval unavailable')
    expect(wrapper.text()).toContain('rerank unavailable')
  })
})
