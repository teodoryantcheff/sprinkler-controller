<script lang="ts">
  import { onMount } from 'svelte'
  import { api, cache, loadPrograms, loadZones, live, type Run } from '../lib/api.svelte'
  import { t, locale, type Key } from '../lib/i18n.svelte'
  import { dur, clock, dateLabel, when, zoneName, programName } from '../lib/format'
  import { toast, errMsg } from '../lib/ui.svelte'
  import Icon from '../lib/Icon.svelte'

  type Ev = { id: number; time: string; kind: string; detail: Record<string, unknown> }
  type Week = { week: string; zone_id: number; minutes: number }

  let tab = $state<'runs' | 'events'>('runs')
  let zone = $state('')
  let program = $state('')
  let outcome = $state('')
  let runs = $state<Run[]>([])
  let events = $state<Ev[]>([])
  let weeks = $state<Week[]>([])
  let more = $state(true)
  const tz = $derived(live.status?.timezone || 'Europe/Sofia')
  const PAGE = 100

  async function loadRuns(reset = true) {
    // eslint-disable-next-line svelte/prefer-svelte-reactivity -- local query builder, not state
    const q = new URLSearchParams({ limit: String(PAGE) })
    if (zone) q.set('zone', zone)
    if (program) q.set('program', program)
    if (outcome) q.set('outcome', outcome)
    if (!reset && runs.length) q.set('before', runs[runs.length - 1].planned_start)
    try {
      const page = await api<Run[]>('GET', '/api/runs?' + q)
      runs = reset ? page : [...runs, ...page]
      more = page.length === PAGE
    } catch (e) {
      toast(errMsg(e), true)
    }
  }
  async function loadEvents() {
    try {
      events = await api<Ev[]>('GET', '/api/log?limit=200')
    } catch (e) {
      toast(errMsg(e), true)
    }
  }

  // Weekly totals: how many weeks back (history is kept for 6 months).
  let range = $state(6)
  async function loadWeeks() {
    weeks = await api<Week[]>('GET', `/api/history/weekly?weeks=${range}`).catch(() => [])
  }

  onMount(() => {
    loadZones()
    loadPrograms()
    loadRuns()
    loadWeeks()
  })

  // Group runs by local day
  const grouped = $derived.by(() => {
    const out: { day: string; label: string; runs: Run[] }[] = []
    for (const r of runs) {
      const iso = r.start || r.planned_start
      const day = new Date(iso).toLocaleDateString('en-CA', { timeZone: tz })
      let g = out[out.length - 1]
      if (!g || g.day !== day) {
        g = { day, label: dateLabel(iso, tz), runs: [] }
        out.push(g)
      }
      g.runs.push(r)
    }
    return out
  })

  // Weekly table: rows = zones, columns = every week in the range (local
  // Mondays, newest last, empty weeks included), with totals.
  const weekCols = $derived.by(() => {
    const now = live.status ? Date.parse(live.status.now) : Date.now()
    const today = new Date(new Date(now).toLocaleDateString('en-CA', { timeZone: tz }) + 'T12:00:00Z')
    const monday = new Date(today.getTime() - ((today.getUTCDay() + 6) % 7) * 86400000)
    const out: string[] = []
    for (let i = range - 1; i >= 0; i--)
      out.push(new Date(monday.getTime() - i * 7 * 86400000).toISOString().slice(0, 10))
    return out
  })
  const weekZones = $derived.by(() => {
    const ids = new Set([...cache.zones.map((z) => z.id), ...weeks.map((w) => w.zone_id)])
    return [...ids].filter((id) => weeks.some((w) => w.zone_id === id)).sort((a, b) => a - b)
  })
  const mins = (z: number | null, w: string | null) =>
    weeks
      .filter((x) => (z == null || x.zone_id === z) && (w == null || x.week === w))
      .reduce((n, x) => n + x.minutes, 0)
  // Whole minutes; under one minute shows "<1" so rows and totals agree.
  const cell = (m: number) => (m <= 0 ? '·' : m < 1 ? '<1' : String(Math.round(m)))

  function eventKind(kind: string) {
    const k = ('ev_' + kind) as Key
    const s = t(k)
    return s === k ? kind.replace(/_/g, ' ') : s
  }

  // Event detail as short readable parts: names instead of ids, local times.
  function eventDetail(d: Record<string, unknown> | null): string {
    if (!d) return ''
    const at = (v: unknown) => when(String(v), tz, new Date(), true)
    const out: string[] = []
    for (const [k, v] of Object.entries(d)) {
      switch (k) {
        case 'zone_id':
          out.push(zoneName(cache.zones, Number(v)))
          break
        case 'program_id':
          out.push(programName(cache.programs, Number(v)))
          break
        case 'planned':
          out.push(`${t('planned')} ${at(v)}`)
          break
        case 'from':
          out.push(`${at(v)} → ${at(d.to)}`)
          break
        case 'to':
          break
        case 'cap_minutes':
        case 'max_minutes':
          out.push(`≤ ${v} ${t('min')}`)
          break
        case 'hours':
          out.push(`${v} ${t('hours_short')}`)
          break
        case 'open_runs':
          out.push(`${t('open_runs')}: ${v}`)
          break
        case 'zones':
        case 'programs':
          out.push(`${t(k)}: ${v}`)
          break
        case 'sensor':
          out.push(v === 'soil' ? t('soil') : t('rain'))
          break
        case 'reason':
          out.push(v === 'controller_off' ? t('reason_controller_off') : v === 'user' ? t('reason_user') : String(v))
          break
        case 'snapshot':
          out.push(`${t('snapshot')} ${v}`)
          break
        default:
          out.push(typeof v === 'object' ? JSON.stringify(v) : String(v))
      }
    }
    return out.join(' · ')
  }

  const outcomes = [
    'done',
    'stopped',
    'skipped_rain',
    'skipped_sensor',
    'skipped_short',
    'skipped_user',
    'cut_max',
    'error',
  ]
  function outcomeClass(o: string) {
    return o === 'done' ? 'ok' : o === 'error' ? 'bad' : o.startsWith('skipped') || o === 'cut_max' ? 'warn' : ''
  }
</script>

<header class="top">
  <h1>{t('history')}</h1>
  <div class="seg" role="group">
    <button
      aria-pressed={tab === 'runs'}
      aria-label={t('runs_tab')}
      title={t('runs_tab')}
      onclick={() => (tab = 'runs')}><Icon name="droplet" size={20} />{t('runs_tab')}</button
    >
    <button
      aria-pressed={tab === 'events'}
      aria-label={t('events_tab')}
      title={t('events_tab')}
      onclick={() => {
        tab = 'events'
        loadEvents()
      }}><Icon name="activity" size={20} />{t('events_tab')}</button
    >
  </div>
</header>

{#if tab === 'runs'}
  <h2 class="row spread">
    <span class="row" style="gap:8px"><Icon name="calendar" />{t('minutes_per_week')}</span>
    <select class="small" style="width:auto" bind:value={range} onchange={loadWeeks} aria-label={t('minutes_per_week')}>
      {#each [6, 12, 26] as n}<option value={n}>{t('weeks_n', { n })}</option>{/each}
    </select>
  </h2>
  <div class="card" style="overflow-x:auto">
    {#if weekZones.length === 0}
      <span class="muted">{t('no_watering_period')}</span>
    {:else}
      <table>
        <thead
          ><tr
            ><th>{t('zone')}</th>{#each weekCols as w}<th class="num"
                >{new Date(w + 'T12:00:00').toLocaleDateString(locale(), { day: 'numeric', month: 'short' })}</th
              >{/each}<th class="num">{t('sum')}</th></tr
          ></thead
        >
        <tbody>
          {#each weekZones as z}
            <tr
              ><td class="ellipsis" style="max-width:9em">{zoneName(cache.zones, z)}</td>{#each weekCols as w}<td
                  class="num">{cell(mins(z, w))}</td
                >{/each}<td class="num"><strong>{cell(mins(z, null))}</strong></td></tr
            >
          {/each}
        </tbody>
        <tfoot>
          <tr
            ><th>{t('sum')}</th>{#each weekCols as w}<th class="num">{cell(mins(null, w))}</th>{/each}<th class="num"
              >{cell(mins(null, null))}</th
            ></tr
          >
        </tfoot>
      </table>
    {/if}
  </div>

  <h2><Icon name="list" />{t('runs_tab')}</h2>
  <div class="row wrap-row filters" style="margin-bottom:10px">
    <select bind:value={zone} onchange={() => loadRuns()} style="width:auto">
      <option value="">{t('all_zones')}</option>
      {#each cache.zones as z}<option value={String(z.id)}>{z.name}</option>{/each}
    </select>
    <select bind:value={program} onchange={() => loadRuns()} style="width:auto">
      <option value="">{t('all_programs')}</option>
      <option value="manual">{t('manual')}</option>
      {#each cache.programs as p}<option value={String(p.id)}>{p.name}</option>{/each}
    </select>
    <select bind:value={outcome} onchange={() => loadRuns()} style="width:auto">
      <option value="">{t('all_outcomes')}</option>
      {#each outcomes as o}<option value={o}>{t(('outcome_' + o) as Key)}</option>{/each}
    </select>
  </div>

  {#each grouped as g (g.day)}
    <h2 style="margin-top:14px">{g.label}</h2>
    <div class="card">
      <ul class="list">
        {#each g.runs as r (r.id)}
          <li class="row spread">
            <span class="tnum small muted" style="width:3.2em">{clock(r.start || r.planned_start, tz)}</span>
            <div class="grow">
              <div class="ellipsis">{zoneName(cache.zones, r.zone_id)}</div>
              <div class="small muted ellipsis">
                {programName(cache.programs, r.program_id)}{r.scale_pct !== 100
                  ? ` · ${t('scaled')} ${r.scale_pct}%`
                  : ''}
              </div>
            </div>
            <div style="text-align:right">
              <div class="tnum">{dur(r.actual_seconds)}</div>
              {#if r.outcome !== 'done'}<span class="chip small {outcomeClass(r.outcome)}"
                  >{t(('outcome_' + r.outcome) as Key)}</span
                >{/if}
            </div>
          </li>
        {/each}
      </ul>
    </div>
  {:else}
    <p class="muted">{t('no_runs')}</p>
  {/each}
  {#if more && runs.length}<div style="margin-top:12px">
      <button style="width:100%" onclick={() => loadRuns(false)}><Icon name="history" />{t('load_more')}</button>
    </div>{/if}
{:else}
  <div class="card">
    <ul class="list">
      {#each events as e (e.id)}
        <li class="row">
          <span class="tnum small muted" style="white-space:nowrap"
            >{new Date(e.time).toLocaleString(locale(), {
              timeZone: tz,
              day: 'numeric',
              month: 'short',
              hour: '2-digit',
              minute: '2-digit',
            })}</span
          >
          <span class="grow"
            ><strong>{eventKind(e.kind)}</strong>
            {#if eventDetail(e.detail)}<span class="small muted"> · {eventDetail(e.detail)}</span>{/if}</span
          >
        </li>
      {:else}
        <li class="muted">{t('events_none')}</li>
      {/each}
    </ul>
  </div>
{/if}

<style>
  .filters select {
    flex: 1 1 140px;
  }
</style>
