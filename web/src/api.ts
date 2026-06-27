// Typed client for the putioarr Web UI REST API (/api/v1). The SPA is served
// same-origin behind HTTP basic auth, so the browser attaches credentials
// automatically; we only manage the short-lived confirmation token for
// destructive operations.

export interface TransferView {
  id: string
  name: string
  label: string
  status: string
  putioStatus: string
  localStatus?: string
  size: number
  downloaded: number
  localDownloaded: number
  progress: number
  downloadSpeed: number
  eta: number
  savePath?: string
  errorMessage?: string
  existsOnPutio: boolean
  downloadedAt?: string
  fileCount: number
}

export interface FileView {
  path: string
  size: number
}

export interface TimelineEvent {
  stage: string
  label: string
  reached: boolean
  current: boolean
  timestamp?: string
}

export interface TransferDetail extends TransferView {
  localPaths: string[]
  files: FileView[]
  timeline: TimelineEvent[]
}

export interface ConfigSnapshot {
  version: string
  downloadDir: string
  targetLabel: string
  maxParallel: number
  pollingInterval: string
  cleanupInterval: string
  keepDownloadedFor: string
  downloadClient: string
  putioSeedRatio: number
  sonarrConfigured: boolean
  radarrConfigured: boolean
}

export interface DeleteScopes {
  putio?: boolean
  local?: boolean
  db?: boolean
}

const BASE = '/api/v1'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    credentials: 'same-origin',
    headers: { Accept: 'application/json', ...(init?.headers ?? {}) },
    ...init,
  })

  if (!res.ok) {
    const message = await extractError(res)
    throw new Error(message)
  }

  if (res.status === 204) {
    return undefined as T
  }

  return (await res.json()) as T
}

async function extractError(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { error?: string }
    if (body?.error) {
      return body.error
    }
  } catch {
    // fall through to status text
  }

  return `${res.status} ${res.statusText}`
}

async function confirmToken(): Promise<string> {
  const body = await request<{ token: string }>('/admin/confirm-token')
  return body.token
}

export async function listTransfers(filters: {
  name?: string
  status?: string
  label?: string
}): Promise<TransferView[]> {
  const params = new URLSearchParams()
  if (filters.name) params.set('name', filters.name)
  if (filters.status) params.set('status', filters.status)
  if (filters.label) params.set('label', filters.label)

  const query = params.toString()
  const body = await request<{ transfers: TransferView[] }>(
    `/transfers${query ? `?${query}` : ''}`,
  )
  return body.transfers ?? []
}

export function getTransfer(id: string): Promise<TransferDetail> {
  return request<TransferDetail>(`/transfers/${encodeURIComponent(id)}`)
}

export function retryTransfer(id: string): Promise<unknown> {
  return request(`/transfers/${encodeURIComponent(id)}/retry`, { method: 'POST' })
}

export function cancelTransfer(id: string): Promise<unknown> {
  return request(`/transfers/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
}

export async function deleteTransfer(id: string, scopes: DeleteScopes): Promise<unknown> {
  const params = new URLSearchParams()
  if (scopes.putio) params.set('putio', '1')
  if (scopes.local) params.set('local', '1')
  if (scopes.db) params.set('db', '1')

  const token = await confirmToken()
  return request(`/transfers/${encodeURIComponent(id)}?${params.toString()}`, {
    method: 'DELETE',
    headers: { 'X-Confirm-Token': token },
  })
}

export async function resetDatabase(): Promise<unknown> {
  const token = await confirmToken()
  return request('/admin/db/reset', {
    method: 'POST',
    headers: { 'X-Confirm-Token': token },
  })
}

export async function purgeDownloads(): Promise<unknown> {
  const token = await confirmToken()
  return request('/admin/downloads/purge', {
    method: 'POST',
    headers: { 'X-Confirm-Token': token },
  })
}

export function getConfig(): Promise<ConfigSnapshot> {
  return request<ConfigSnapshot>('/config')
}
