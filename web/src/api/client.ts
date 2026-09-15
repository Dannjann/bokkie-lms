/**
 * Thin fetch wrapper for the bokkie API.
 *
 * Everything is same-origin: the SPA is served by the same Go binary that
 * serves the API, so there is no base URL to configure and no CORS to think
 * about. In dev the Go server proxies the SPA from Vite, which keeps that true.
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

export async function getJson<T>(path: string): Promise<T> {
  const response = await fetch(path, { headers: { Accept: 'application/json' } })
  const requestId = response.headers.get('X-Request-Id')

  if (!response.ok) {
    const body = (await response.json().catch(() => ({}))) as ErrorBody
    throw new ApiError(body.error ?? response.statusText, response.status, body.request_id ?? requestId)
  }
  return (await response.json()) as T
}
