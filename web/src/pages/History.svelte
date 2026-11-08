<script lang="ts">
  import { onMount } from 'svelte'
  import { api, cache, loadPrograms, loadZones, live, type Run } from '../lib/api.svelte'
  import { t, locale, type Key } from '../lib/i18n.svelte'
  import { dur, clock, dateLabel, zoneName, programName } from '../lib/format'
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

  onMount(async () => {
    loadZones()
    loadPrograms()
    loadRuns()
    weeks = await api<Week[]>('GET', '/api/history/weekly?weeks=10').catch(() => [])
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

  // Weekly table: rows = zones, columns = last weeks
  const weekCols = $derived([...new Set(weeks.map((w) => w.week))].sort().slice(-6))
  const weekZones = $derived([...new Set(weeks.map((w) => w.zone_id))].sort((a, b) => a - b))
  function weekMin(z: number, w: string) {
    const m = weeks.find((x) => x.zone_id === z && x.week === w)?.minutes
    return m ? Math.round(m) : ''
  }

  const outcomes = ['done', 'stopped', 'skipped_rain', 'skipped_sensor', 'skipped_short', 'cut_max', 'error']
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
  {#if weekCols.length}
    <h2><Icon name="calendar" />{t('minutes_per_week')}</h2>
    <div class="card" style="overflow-x:auto">
      <table>
        <thead
          ><tr
            ><th>{t('zone')}</th>{#each weekCols as w}<th class="num"
                >{new Date(w + 'T12:00:00').toLocaleDateString(locale(), { day: 'numeric', month: 'short' })}</th
              >{/each}</tr
          ></thead
        >
        <tbody>
          {#each weekZones as z}
            <tr
              ><td class="ellipsis" style="max-width:9em">{zoneName(cache.zones, z)}</td>{#each weekCols as w}<td
                  class="num">{weekMin(z, w)}</td
                >{/each}</tr
            >
          {/each}
        </tbody>
      </table>
    </div>
  {/if}

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
            ><strong>{e.kind.replace(/_/g, ' ')}</strong>
            {#if e.detail && Object.keys(e.detail).length}<span class="small muted">
                {JSON.stringify(e.detail)}</span
              >{/if}</span
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
