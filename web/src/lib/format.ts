import { t, locale, type Key } from './i18n.svelte'
import type { Program, Zone, StartRule } from './api.svelte'

export const WEEKDAYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const

export function dur(seconds: number): string {
  seconds = Math.max(0, Math.round(seconds))
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = seconds % 60
  if (h) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
  if (m && !s) return `${m} ${t('min')}`
  if (m) return `${m}:${String(s).padStart(2, '0')}`
  return `${s} ${t('sec')}`
}

export function minutes(seconds: number): string {
  const m = seconds / 60
  return `${Number.isInteger(m) ? m : m.toFixed(1)} ${t('min')}`
}

export function clock(iso: string, tz: string): string {
  return new Date(iso).toLocaleTimeString(locale(), { hour: '2-digit', minute: '2-digit', timeZone: tz })
}

function dayKey(d: Date, tz: string) {
  return d.toLocaleDateString('en-CA', { timeZone: tz })
}

export function when(iso: string, tz: string, now = new Date()): string {
  const d = new Date(iso)
  const k = dayKey(d, tz)
  const today = dayKey(now, tz)
  const tomorrow = dayKey(new Date(now.getTime() + 86400000), tz)
  let day: string
  if (k === today) day = t('today')
  else if (k === tomorrow) day = t('tomorrow')
  else day = d.toLocaleDateString(locale(), { weekday: 'short', day: 'numeric', month: 'short', timeZone: tz })
  return `${day} ${clock(iso, tz)}`
}

export function dateLabel(iso: string, tz: string): string {
  return new Date(iso).toLocaleDateString(locale(), { weekday: 'long', day: 'numeric', month: 'long', timeZone: tz })
}

export function timeSpecLabel(s: string): string {
  const m = /^(sunrise|sunset)([+-]\d+)?$/.exec(s)
  if (!m) return s
  const off = m[2] ? Number(m[2]) : 0
  const ev = t(m[1] as Key)
  if (!off) return ev
  return `${ev} ${off < 0 ? '−' : '+'}${Math.abs(off)} ${t('min')}`
}

export function startSummary(r: StartRule): string {
  switch (r.kind) {
    case 'fixed': return (r.times || []).map(timeSpecLabel).join(', ')
    case 'solar': return timeSpecLabel(`${r.event}${r.offset_min ? (r.offset_min > 0 ? '+' : '') + r.offset_min : ''}`)
    case 'repeat': return `${timeSpecLabel(r.first || '')} +${r.count}× ${r.every_min} ${t('min')}`
  }
}

export function daySummary(p: Program): string {
  const r = p.day_rule
  let s = ''
  if (r.kind === 'weekdays') {
    const d = r.days || []
    s = d.length === 7 ? t('every_day') : WEEKDAYS.filter((w) => d.includes(w)).map((w) => t(w)).join(', ')
  } else if (r.kind === 'interval') {
    s = r.every === 1 ? t('every_day') : `${t('every')} ${r.every} ${t('days_unit')}`
  }
  if (r.parity) s = (s ? s + ' · ' : '') + t(r.parity === 'odd' ? 'odd' : 'even')
  if (p.season_from && p.season_to) s += ` · ${p.season_from} – ${p.season_to}`
  return s
}

export function programSummary(p: Program, zones: Zone[]): string {
  const zn = (id: number) => zones.find((z) => z.id === id)?.name ?? `#${id}`
  const z = p.zones.map((pz) => `${zn(pz.zone_id)} ${minutes(pz.seconds)}`).join(' → ')
  return `${daySummary(p)} ${t('at')} ${startSummary(p.start_rule)} · ${z}`
}

export function zoneName(zones: Zone[], id: number): string {
  return zones.find((z) => z.id === id)?.name ?? `#${id}`
}

export function programName(programs: Program[], id: number | null): string {
  if (id == null) return t('manual')
  return programs.find((p) => p.id === id)?.name ?? `#${id}`
}
