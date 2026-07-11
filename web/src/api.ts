import type { Bundle, Dashboard, Revision } from './types'

export class APIError extends Error {
  constructor(message: string, readonly status: number) {
    super(message)
    this.name = 'APIError'
  }
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !(init.body instanceof FormData)) headers.set('Content-Type', 'application/json')
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (!response.ok) {
    const body = await response.json().catch(() => ({ error: response.statusText }))
    throw new APIError(body.error || response.statusText, response.status)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export const api = {
  bootstrap: () => request<{ needs_setup: boolean }>('/api/v1/bootstrap'),
  login: (body: object) => request('/api/v1/session', { method: 'POST', body: JSON.stringify(body) }),
  logout: () => request('/api/v1/session', { method: 'DELETE' }),
  me: () => request<{ username: string }>('/api/v1/me'),
  dashboard: () => request<Dashboard>('/api/v1/dashboard'),
  draft: () => request<Revision>('/api/v1/draft'),
  saveDraft: (bundle: Bundle) => request<Revision>('/api/v1/draft', { method: 'PUT', body: JSON.stringify(bundle) }),
  validate: () => request<{ revision_id: number; rule_count: number; warnings: unknown[] }>('/api/v1/draft/validate', { method: 'POST' }),
  apply: () => request<Revision>('/api/v1/apply', { method: 'POST' }),
  revisions: () => request<{ revisions: Revision[] }>('/api/v1/revisions'),
  rollback: (id: number) => request<Revision>(`/api/v1/revisions/${id}/rollback`, { method: 'POST' }),
  logs: () => request<{ logs: string }>('/api/v1/logs?lines=500'),
  diagnostics: () => request<{ processes: { name: string; pid: number }[]; logs: string }>('/api/v1/diagnostics'),
  ios: () => request<Record<string, string>>('/api/v1/ios/profile'),
  changePassword: (body: object) => request('/api/v1/password', { method: 'POST', body: JSON.stringify(body) }),
  updateCheck: () => request<{ current: string; latest: string; available: boolean }>('/api/v1/update'),
  updateApply: () => request<{ current: string; latest: string; available: boolean }>('/api/v1/update', { method: 'POST' }),
}
