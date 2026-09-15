/**
 * Thin fetch wrapper for the bokkie API.
 *
 * Everything is same-origin: the SPA is served by the same Go binary that
 * serves the API, so there is no base URL to configure and no CORS to think
 * about. In dev the Go server proxies the SPA from Vite, which keeps that true.
 *
 * The session cookie rides along automatically for the same reason — there is
 * no token to store in localStorage, which is deliberate: an HttpOnly cookie
 * cannot be read by a script, and this app runs on shared counter PCs.
 */

/** ApiError carries the server's request ID, which is what the log is searched by. */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly requestId: string | null,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

interface ErrorBody {
  error?: string
  request_id?: string
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const response = await fetch(path, {
    method,
    headers: body === undefined
      ? { Accept: 'application/json' }
      : { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const requestId = response.headers.get('X-Request-Id')

  if (!response.ok) {
    const errorBody = (await response.json().catch(() => ({}))) as ErrorBody
    throw new ApiError(
      errorBody.error ?? response.statusText,
      response.status,
      errorBody.request_id ?? requestId,
    )
  }

  // 204 and friends have no body to parse.
  if (response.status === 204 || response.headers.get('Content-Length') === '0') {
    return undefined as T
  }
  return (await response.json()) as T
}

export function getJson<T>(path: string): Promise<T> {
  return request<T>('GET', path)
}

export function postJson<T>(path: string, body?: unknown): Promise<T> {
  return request<T>('POST', path, body ?? {})
}

export function putJson<T>(path: string, body: unknown): Promise<T> {
  return request<T>('PUT', path, body)
}

export function deleteJson<T>(path: string): Promise<T> {
  return request<T>('DELETE', path)
}
