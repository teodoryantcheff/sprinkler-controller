// API client, live state over server-sent events, and shared types.

export interface Zone {
  id: number; output_index: number; name: string; enabled: boolean; group_id: number
  uses_master: boolean; ignore_rain: boolean; ignore_sensor: boolean; max_minutes: number; sort_order: number
}
export interface DayRule { kind: 'weekdays' | 'interval' | 'oddeven'; days?: string[]; every?: number; anchor?: string; parity?: '' | 'odd' | 'even'; skip31?: boolean }
export interface StartRule { kind: 'fixed' | 'repeat' | 'solar'; times?: string[]; first?: string; every_min?: number; count?: number; event?: 'sunrise' | 'sunset'; offset_min?: number }
export interface ProgramZone { zone_id: number; seconds: number }
export interface Program {
  id: number; name: string; enabled: boolean; day_rule: DayRule; start_rule: StartRule
  season_from: string; season_to: string; weather_scaled: boolean; sort_order: number; zones: ProgramZone[]
}
export interface Run {
  id: number; zone_id: number; program_id: number | null; planned_start: string; start: string | null; end: string | null
  planned_seconds: number; actual_seconds: number; scale_pct: number; outcome: string
}
export interface RunView {
  run_id: number; zone_id: number; program_id: number | null; manual: boolean; state: string
  seconds: number; remaining: number; est_start: string; est_end: string
}
export interface PlannedRun { program_id: number; zone_id: number; planned_start: string; planned_seconds: number; seconds: number; scale_pct: number; skip?: string; est_start: string }
export interface Status {
  now: string
  runner: { paused: boolean; fault: string; master_on: boolean; outputs: boolean[]; driver: string; running: RunView[]; queue: RunView[] }
  next: PlannedRun[]; watering_level: number; watering_source: string; rain_delay_until: string | null
  sensor: { enabled: boolean; wet: boolean; raw: boolean; error?: string; sim: boolean; kind: string }
  controller_on: boolean; timezone: string; sun?: { sunrise: string; sunset: string }
}
export interface Settings {
  device_name: string; timezone: string; lat: number; lon: number; station_delay_s: number
  master_zone_id: number; master_on_delay_s: number; master_off_delay_s: number; watering_level_pct: number
  rain_delay_until: string | null
  sensor: { enabled: boolean; kind: string; normally: string; on_delay_s: number; off_delay_s: number }
  max_open_zones: number; daily_cap_minutes: number; lan_no_login: boolean; language: 'en' | 'bg'; controller_on: boolean
}
export interface Me { user: string; logged_in: boolean; setup_required: boolean; language: string; device_name: string }

export const session = $state({ checked: false, loggedIn: false, setupRequired: false, deviceName: '' })

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message) }
}

export async function api<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, headers: {}, credentials: 'same-origin' }
  if (body !== undefined) {
    init.body = JSON.stringify(body)
    ;(init.headers as Record<string, string>)['Content-Type'] = 'application/json'
  }
  const r = await fetch(path, init)
  const data = await r.json().catch(() => ({}))
  if (!r.ok) {
    if (r.status === 401) { session.loggedIn = false; stopLive() }
    throw new ApiError(r.status, (data as { error?: string }).error || r.statusText)
  }
  return data as T
}

export const live = $state<{ status: Status | null; connected: boolean; clockSkewMs: number }>({ status: null, connected: false, clockSkewMs: 0 })

// Data that several pages share; refreshed on demand.
export const cache = $state<{ zones: Zone[]; programs: Program[] }>({ zones: [], programs: [] })

export async function loadZones() { cache.zones = await api<Zone[]>('GET', '/api/zones') }
export async function loadPrograms() { cache.programs = await api<Program[]>('GET', '/api/programs') }

let es: EventSource | null = null
let retry: number | undefined

export function startLive() {
  stopLive()
  es = new EventSource('/api/stream')
  es.addEventListener('state', (e) => {
    const s = JSON.parse((e as MessageEvent).data) as Status
    live.status = s
    live.connected = true
    live.clockSkewMs = Date.parse(s.now) - Date.now()
  })
  es.onerror = () => {
    live.connected = false
    es?.close()
    es = null
    retry = window.setTimeout(async () => {
      try { await checkSession(); if (session.loggedIn) startLive() } catch { startLive() }
    }, 3000)
  }
}

export function stopLive() {
  es?.close()
  es = null
  if (retry) clearTimeout(retry)
}

export async function checkSession() {
  const me = await api<Me>('GET', '/api/me')
  session.checked = true
  session.loggedIn = me.logged_in
  session.setupRequired = me.setup_required
  session.deviceName = me.device_name
  return me
}
