import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './client'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('api client', () => {
  it('returns parsed JSON on success', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify({ configs: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    )

    const payload = await api.get('/api/v1/rag-configs')
    expect(payload).toEqual({ configs: [] })
  })

  it('normalizes network failures as unreachable with an actionable message', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => {
      throw new TypeError('Failed to fetch')
    }))

    await expect(api.get('/api/v1/rag-configs')).rejects.toMatchObject({
      kind: 'unreachable',
      code: 'unreachable',
    })
    await api.get('/api/v1/rag-configs').catch((err) => {
      expect(err.message).toMatch(/docker compose ps backend/)
    })
  })

  it('parses the structured error envelope on HTTP errors', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(
          JSON.stringify({
            error: {
              code: 'name_conflict',
              message: 'a configuration with this name already exists',
            },
          }),
          { status: 409, headers: { 'Content-Type': 'application/json' } },
        ),
      ),
    )

    await expect(api.post('/api/v1/rag-configs', {})).rejects.toMatchObject({
      kind: 'http',
      status: 409,
      code: 'name_conflict',
      message: 'a configuration with this name already exists',
    })
  })

  it('falls back to a generic message for non-JSON error bodies', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('<html>proxy error</html>', { status: 502 })),
    )

    await expect(api.get('/api/v1/rag-configs')).rejects.toMatchObject({
      kind: 'http',
      status: 502,
      code: 'http_error',
    })
  })

  it('sends JSON bodies with the JSON content type', async () => {
    const fetchMock = vi.fn(async () => new Response('{}', { status: 201 }))
    vi.stubGlobal('fetch', fetchMock)

    await api.post('/api/v1/rag-configs', { name: 'x' })

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/rag-configs', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: 'x' }),
    })
  })
})
