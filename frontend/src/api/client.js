// The single API client shared by the whole shell. Every backend call goes
// through here so error handling stays uniform: failures are normalized to
// { kind, status, code, message, fields, payload } objects.
//
// kind 'unreachable'  — the network request itself failed (backend down)
// kind 'http'         — the backend answered with a non-2xx status
//
// Provider credentials never appear here: the browser only ever talks to the
// Go API, which holds keys server-side.

function unreachableError() {
  return {
    kind: 'unreachable',
    code: 'unreachable',
    message:
      'Cannot reach the API. Check that the backend container is running (docker compose ps backend) and that port 8080 is up, then retry.',
  }
}

function httpError(status, body) {
  const envelope = body && typeof body === 'object' && body.error ? body.error : null
  let message =
    envelope?.message ??
    `API returned HTTP ${status} without a structured error body.`
  if (status >= 502 && !envelope) {
    message +=
      ' The API is unreachable behind the proxy — check that the backend container is running (docker compose ps backend) and retry.'
  }
  return {
    kind: 'http',
    status,
    code: envelope?.code ?? 'http_error',
    message,
    fields: envelope?.fields ?? [],
    trace_id: envelope?.trace_id ?? '',
    payload: body,
  }
}

async function request(path, { method = 'GET', body } = {}) {
  const options = { method, headers: {} }
  if (body !== undefined) {
    if (body instanceof FormData) {
      options.body = body
    } else {
      options.headers['Content-Type'] = 'application/json'
      options.body = JSON.stringify(body)
    }
  }

  let response
  try {
    response = await fetch(path, options)
  } catch {
    throw unreachableError()
  }

  let payload = null
  if (response.status !== 204) {
    try {
      payload = await response.json()
    } catch {
      payload = null
    }
  }

  if (!response.ok) {
    throw httpError(response.status, payload)
  }
  return payload
}

export const api = {
  get: (path) => request(path),
  post: (path, body) => request(path, { method: 'POST', body }),
  delete: (path) => request(path, { method: 'DELETE' }),
}
