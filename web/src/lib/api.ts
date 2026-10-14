export type Upstream = {
  id: string; name: string; base_url: string; region: string;
  auth_mode: "api_key" | "sigv4"; signing_service: string; profile?: string;
  project?: string; weight: number; enabled: boolean; models: Record<string, string>;
  has_credentials: boolean; created_at: string;
}
export type Health = { status: string; checked_at: string; cooldown_until: string; latency_ms: number; message?: string }
export type APIKey = { id: string; name: string; prefix: string; created_at: string; user_id: string; revoked_at?: string }
export type User = { id: string; name: string; note: string; enabled: boolean; created_at: string }
export type SessionInfo = { role: "admin" | "user"; user?: User; key?: APIKey }
export type UsageTotals = {
  requests: number; errors: number; input_tokens: number; output_tokens: number; total_tokens: number;
  cached_tokens: number; reasoning_tokens: number; known_usage_requests: number; missing_usage_requests: number; in_flight_requests: number; pending_sync_requests?: number;
}
export type UsageGroup = UsageTotals & { id: string; name: string; user_id?: string }
export type DailyModelUsage = UsageTotals & { date: string; model: string }
export type UsageReport = {
  from: string; to: string; all: boolean; timezone_offset: number; totals: UsageTotals;
  by_user: UsageGroup[]; by_key: UsageGroup[]; by_model: UsageGroup[]; by_upstream?: UsageGroup[]; daily: UsageGroup[]; daily_by_model?: DailyModelUsage[];
}
export type Settings = { request_timeout_seconds: number; max_retries: number; cooldown_seconds: number; log_capacity: number; allow_http_upstreams: boolean }
export type RequestLog = {
  id: string; time: string; method: string; path: string; model: string; upstream: string;
  upstream_name: string; key_name: string; status: number; duration_ms: number;
  stream: boolean; attempts: number; input_tokens: number; output_tokens: number; error?: string;
  user_id: string; user_name: string; key_id: string; usage_known: boolean; inference: boolean;
}
export type Metrics = {
  started_at: string; requests: number; errors: number; tokens: number; avg_latency_ms: number;
  recent: RequestLog[]; timeline: { time: string; requests: number; errors: number }[]; health: Record<string, Health>;
  usage_storage_healthy: boolean;
}
export type Model = { id: string; object: string; owned_by: string }

export const session = {
  get: () => sessionStorage.getItem("mantle-admin") || "",
  set: (token: string) => sessionStorage.setItem("mantle-admin", token),
  clear: () => sessionStorage.removeItem("mantle-admin"),
}

export async function errorMessage(response: Response): Promise<string> {
  try {
    const data = await response.json()
    return data.error?.message || `请求失败（HTTP ${response.status}）`
  } catch { return `请求失败（HTTP ${response.status}）` }
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  return request<T>(`/api/admin${path}`, options)
}
export function selfApi<T>(path: string, options: RequestInit = {}): Promise<T> {
  return request<T>(`/api/me${path}`, options)
}
export function getSession() { return request<SessionInfo>("/api/session") }

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...options,
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${session.get()}`, ...options.headers },
  })
  if (!response.ok) {
    if (response.status === 401) window.dispatchEvent(new Event("mantle:unauthorized"))
    throw new Error(await errorMessage(response))
  }
  return response.json()
}
export function message(error: unknown) { return error instanceof Error ? error.message : "操作失败，请重试" }

export type RestoreResult = { restored: boolean; usage_only: boolean; snapshot_at: string; users: number; api_keys: number; upstreams: number; usage_requests: number }

async function backupRequest(path: string, options: RequestInit): Promise<Response> {
  const response = await fetch(`/api/admin/backup/${path}`, {
    ...options,
    headers: { Authorization: `Bearer ${session.get()}`, ...options.headers },
  })
  if (!response.ok) {
    if (response.status === 401) window.dispatchEvent(new Event("mantle:unauthorized"))
    throw new Error(await errorMessage(response))
  }
  return response
}

export async function exportBackup(password: string): Promise<void> {
  const response = await backupRequest("export", {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ password }),
  })
  const url = URL.createObjectURL(await response.blob())
  const link = document.createElement("a")
  link.href = url
  link.download = `mantle-mask-${new Date().toISOString().replace(/[:.]/g, "-")}.mmbak`
  document.body.appendChild(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export async function restoreBackup(file: File, password: string, usageOnly = false): Promise<RestoreResult> {
  const body = new FormData()
  body.set("file", file)
  body.set("password", password)
  body.set("confirm", "RESTORE")
  body.set("mode", usageOnly ? "usage" : "full")
  const response = await backupRequest("restore", { method: "POST", body })
  return response.json()
}
