import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import LibraryView from './LibraryView.vue'
import { api } from '../api/client'

vi.mock('../api/client', () => ({ api: { get: vi.fn(), post: vi.fn(), delete: vi.fn() } }))

const config = { id: 'cfg', name: 'Baseline', chunk_size: 500, chunk_overlap: 80 }
const failed = { id: 'doc', filename: 'policy.txt', size_bytes: 42, status: 'failed', chunk_count: 3,
  active_revision_id: 'old', latest_revision_id: 'new', job: { state: 'failed', stage: 'embed', error_code: 'embedding_failed', error_message: 'Batch 2: controlled HTTP 429' } }
let wrapper

beforeEach(() => { vi.clearAllMocks() })
afterEach(() => { wrapper?.unmount() })

function setup(documents = []) {
  api.get.mockImplementation(path => Promise.resolve(path.includes('rag-configs') ? { configs: [config] } : { documents }))
  wrapper = mount(LibraryView)
  return flushPromises()
}

describe('Library lifecycle', () => {
  it('shows empty state and submits the actual file and saved config as multipart', async () => {
    await setup()
    expect(wrapper.find('[data-state="empty"]').exists()).toBe(true)
    const file = new File(['Approval evidence'], 'source.txt', { type: 'text/plain' })
    const input = wrapper.find('input[type=file]')
    Object.defineProperty(input.element, 'files', { value: [file] })
    await input.trigger('change')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    const [path, body] = api.post.mock.calls[0]
    expect(path).toBe('/api/v1/documents')
    expect(body.get('file').name).toBe('source.txt')
    expect(body.get('config_id')).toBe('cfg')
    expect(wrapper.text()).toContain('Document queued')
  })

  it('shows actual replacement error and preserved searchable count, then reprocesses', async () => {
    await setup([failed])
    expect(wrapper.text()).toContain('Batch 2: controlled HTTP 429')
    expect(wrapper.text()).toContain('Previous revision remains searchable')
    expect(wrapper.find('tbody').text()).toContain('3')
    await wrapper.findAll('button').find(b => b.text() === 'Reprocess').trigger('click')
    await flushPromises()
    expect(api.post).toHaveBeenCalledWith('/api/v1/documents/doc/reprocess', { config_id: 'cfg' })
  })

  it('requires a delete confirmation and refreshes after deletion', async () => {
    await setup([failed])
    await wrapper.findAll('button').find(b => b.text() === 'Delete').trigger('click')
    expect(api.delete).not.toHaveBeenCalled()
    expect(wrapper.find('[role=alertdialog]').text()).toContain('historical evidence')
    await wrapper.findAll('button').find(b => b.text() === 'Confirm delete').trigger('click')
    await flushPromises()
    expect(api.delete).toHaveBeenCalledWith('/api/v1/documents/doc')
  })

  it('exposes recoverable dispatch failures without allowing duplicate active ingestion', async () => {
    await setup([{ ...failed, job: { ...failed.job, state: 'dispatch_failed', stage: 'dispatch', error_code: 'dispatch_failed' } }])
    expect(wrapper.findAll('button').find(b => b.text() === 'Reprocess').attributes('disabled')).toBeDefined()
    await wrapper.findAll('button').find(b => b.text() === 'Retry dispatch').trigger('click')
    await flushPromises()
    expect(api.post).toHaveBeenCalledWith('/api/v1/documents/doc/retry-dispatch')
  })

  it('surfaces unavailable Library errors with retry', async () => {
    api.get.mockRejectedValue({ code: 'unreachable', message: 'Cannot reach API' })
    wrapper = mount(LibraryView)
    await flushPromises()
    expect(wrapper.find('[role=alert]').text()).toContain('Cannot reach API')
    expect(wrapper.findAll('button').some(b => b.text() === 'Retry')).toBe(true)
  })
})
