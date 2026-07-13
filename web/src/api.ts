import type { ActiveRules, ApplyOperation, Bundle, Dashboard, Diagnostics, IOSProfile, Metric, RuleFile } from './types'

export class APIError extends Error {
  constructor(message: string, readonly status: number) {
    super(message)
    this.name = 'APIError'
  }
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
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
	metrics: () => request<{ metrics: Metric[] }>('/api/v1/metrics'),
	runDiagnostics: (scope = 'all') => request<Diagnostics>(`/api/v1/diagnostics/run?scope=${encodeURIComponent(scope)}`, { method: 'POST' }),
  activeRules: () => request<ActiveRules>('/api/v1/active/rules'),
  defaultRules: () => request<RuleFile>('/api/v1/rules/defaults'),
  config: () => request<Bundle>('/api/v1/config'),
  validateConfig: (bundle: Bundle) => request<{ rule_count: number; warnings: unknown[] }>('/api/v1/config/validate', { method: 'POST', body: JSON.stringify(bundle) }),
  applyConfig: (bundle: Bundle, operationID: string) => request<ApplyOperation>('/api/v1/config/apply', { method: 'POST', body: JSON.stringify(bundle), headers: { 'X-5gws-Operation-ID': operationID } }),
  applyStatus: (operationID: string) => request<ApplyOperation>(`/api/v1/config/apply/${encodeURIComponent(operationID)}`),
  importConfig: (content: string) => request<Bundle>('/api/v1/config/import', { method: 'POST', body: content, headers: { 'Content-Type': 'application/toml' } }),
  logs: () => request<{ logs: string }>('/api/v1/logs?lines=500'),
  diagnostics: () => request<{ processes: { name: string; pid: number }[]; logs: string }>('/api/v1/diagnostics'),
	ios: () => request<IOSProfile>('/api/v1/ios/profile'),
  changePassword: (body: object) => request('/api/v1/password', { method: 'POST', body: JSON.stringify(body) }),
  updateCheck: () => request<{ current: string; latest: string; available: boolean }>('/api/v1/update'),
  updateApply: () => request<{ current: string; latest: string; available: boolean }>('/api/v1/update', { method: 'POST' }),
}
