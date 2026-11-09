<script lang="ts">
  import { onMount } from 'svelte'
  import { api, cache, live, loadPrograms, loadZones, type PlannedRun } from '../lib/api.svelte'
  import { t, locale, type Key } from '../lib/i18n.svelte'
  import { dur, clock, zoneName, programName } from '../lib/format'
  import { toast, errMsg, go } from '../lib/ui.svelte'
  import Icon from '../lib/Icon.svelte'

  // One program start: the zone runs it expands to, and the time window
  // its valves are estimated to be open.
  type Start = {
    key: string
    programId: number
    planned: string
    runs: PlannedRun[]
    from: number // ms
    to: number // ms
    skipped: boolean // skipped by the user
    blocked: string // all runs skipped for another reason (rain delay, too short …)
    overlap: boolean
  }

  const tz = $derived(live.status?.timezone || 'Europe/Sofia')
  let runs = $state<PlannedRun[]>([])
  let loaded = $state(false)
  let open = $state('')
  let busy = $state(false)

  async function load() {
    try {
      runs = await api<PlannedRun[]>('GET', '/api/upcoming?days=7')
    } catch (e) {
      toast(errMsg(e), true)
    } finally {
      loaded = true
    }
  }
  onMount(() => {
    loadZones()
    loadPrograms()
    load()
    const id = setInterval(load, 60000)
    return () => clearInterval(id)
  })

  const dayKey = (ms: number) => new Date(ms).toLocaleDateString('en-CA', { timeZone: tz })
  function minuteOfDay(ms: number) {
    const [h, m] = new Date(ms)
      .toLocaleTimeString('en-GB', { timeZone: tz, hour: '2-digit', minute: '2-digit', hour12: false })
      .split(':')
      .map(Number)
    return (h % 24) * 60 + m
  }

  const starts = $derived.by(() => {
    // eslint-disable-next-line svelte/prefer-svelte-reactivity -- local grouping, not state
    const by = new Map<string, Start>()
    for (const r of runs) {
      const key = `${r.program_id}@${r.planned_start}`
      let s = by.get(key)
      if (!s) {
        const at = Date.parse(r.planned_start)
        s = {
          key,
          programId: r.program_id,
          planned: r.planned_start,
          runs: [],
          from: at,
          to: at,
          skipped: false,
          blocked: '',
          overlap: false,
        }
        by.set(key, s)
      }
      s.runs.push(r)
    }
    const out = [...by.values()]
    for (const s of out) {
      s.skipped = s.runs.every((r) => r.skip === 'skipped_user')
      const live = s.runs.filter((r) => !r.skip)
      if (!s.skipped && live.length === 0) s.blocked = s.runs[0]?.skip || ''
      if (live.length) {
        s.from = Math.min(...live.map((r) => Date.parse(r.est_start)))
        s.to = Math.max(...live.map((r) => Date.parse(r.est_start) + r.seconds * 1000))
      }
    }
    // Overlaps: two starts that will water at the same time.
    const active = out.filter((s) => !s.skipped && !s.blocked)
    for (const a of active) for (const b of active) if (a !== b && a.from < b.to && b.from < a.to) a.overlap = true
    return out.sort((a, b) => a.from - b.from)
  })

  // The next 7 local days, each with its starts (empty days included).
  const days = $derived.by(() => {
    const now = live.status ? Date.parse(live.status.now) : Date.now()
    const out: { key: string; label: string; starts: Start[]; seconds: number }[] = []
    for (let i = 0; i < 7; i++) {
      const ms = now + i * 86400000
      const key = dayKey(ms)
      if (out.some((d) => d.key === key)) continue
      const label =
        i === 0
          ? t('today')
          : i === 1
            ? t('tomorrow')
            : new Date(ms).toLocaleDateString(locale(), {
                weekday: 'long',
                day: 'numeric',
                month: 'short',
                timeZone: tz,
              })
      const ss = starts.filter((s) => dayKey(s.from) === key)
      const seconds = ss
        .filter((s) => !s.skipped && !s.blocked)
        .reduce((n, s) => n + s.runs.filter((r) => !r.skip).reduce((m, r) => m + r.seconds, 0), 0)
      out.push({ key, label, starts: ss, seconds })
    }
    return out
  })

  // A stable colour per program, readable in light and dark themes.
  const color = (id: number) => `hsl(${(id * 67 + 140) % 360} 55% 48%)`

  function bar(s: Start) {
    const left = (minuteOfDay(s.from) / 1440) * 100
    const width = Math.max(((s.to - s.from) / 60000 / 1440) * 100, 0.8)
    return `left:${left}%;width:${Math.min(width, 100 - left)}%;background:${color(s.programId)}`
  }

  function zonesLabel(s: Start) {
    const n = s.runs.length
    return n === 1 ? t('zone_1') : t('zones_n', { n })
  }
  function total(s: Start) {
    return s.runs.filter((r) => !r.skip).reduce((m, r) => m + r.seconds, 0)
  }

  async function setSkip(s: Start, skip: boolean) {
    const name = programName(cache.programs, s.programId)
    if (skip && !confirm(t('confirm_skip', { name, t: clock(s.planned, tz) }))) return
    busy = true
    try {
      await api('POST', '/api/skip', { program_id: s.programId, planned_start: s.planned, skip })
      toast(skip ? t('skip_done') : t('unskip_done'))
      await load()
    } catch (e) {
      toast(errMsg(e), true)
    } finally {
      busy = false
    }
  }

  function select(s: Start) {
    open = open === s.key ? '' : s.key
    if (open) document.getElementById('start-' + s.key)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }
</script>

<header class="top">
  <h1>{t('week')}</h1>
  <div class="seg" role="group">
    <button aria-pressed="false" onclick={() => go('/programs')}><Icon name="list" size={20} />{t('list_view')}</button>
    <button aria-pressed="true"><Icon name="calendar" size={20} />{t('week')}</button>
  </div>
</header>

{#if !loaded}
  <p class="muted">{t('loading')}</p>
{:else}
  {#each days as d (d.key)}
    <h2 class="row spread">
      <span>{d.label}</span>
      {#if d.seconds}<span class="small muted tnum">{dur(d.seconds)}</span>{/if}
    </h2>
    <div class="card">
      <div class="track" aria-hidden="true">
        {#each [6, 12, 18] as h}<i class="tick" style="left:{(h / 24) * 100}%"><span>{h}</span></i>{/each}
        {#each d.starts as s (s.key)}
          <button
            class="seg-bar"
            class:skipped={s.skipped || !!s.blocked}
            class:overlap={s.overlap}
            style={bar(s)}
            tabindex="-1"
            title={`${programName(cache.programs, s.programId)} ${clock(s.planned, tz)}`}
            onclick={() => select(s)}
          ></button>
        {/each}
      </div>
      {#if d.starts.length === 0}
        <div class="small muted" style="margin-top:8px">{t('nothing_planned')}</div>
      {:else}
        <ul class="list" style="margin-top:6px">
          {#each d.starts as s (s.key)}
            <li id={'start-' + s.key} class:sel={open === s.key}>
              <div class="row spread">
                <span class="dot" style="background:{color(s.programId)}"></span>
                <button class="grow plain" onclick={() => select(s)} aria-expanded={open === s.key}>
                  <div class="ellipsis" class:muted={s.skipped || !!s.blocked} class:strike={s.skipped}>
                    <span class="tnum">{clock(s.planned, tz)}</span> · {programName(cache.programs, s.programId)}
                  </div>
                  {#if s.skipped || s.blocked}
                    <div class="small ellipsis warn-text">
                      {s.skipped ? t('skipped_by_you') : t(('outcome_' + s.blocked) as Key)}
                    </div>
                  {:else}
                    <div class="small muted ellipsis">
                      {zonesLabel(s)}{total(s) ? ` · ${dur(total(s))}` : ''}{s.to > s.from
                        ? ` · → ${clock(new Date(s.to).toISOString(), tz)}`
                        : ''}
                    </div>
                  {/if}
                </button>
                {#if s.overlap && !s.skipped && !s.blocked}
                  <span class="chip small warn"><Icon name="alert" size={16} />{t('overlap')}</span>
                {/if}
                {#if s.skipped}
                  <button class="small" disabled={busy} onclick={() => setSkip(s, false)}
                    ><Icon name="play" size={18} />{t('unskip')}</button
                  >
                {:else}
                  <button class="small" disabled={busy} onclick={() => setSkip(s, true)}
                    ><Icon name="x" size={18} />{t('skip')}</button
                  >
                {/if}
              </div>
              {#if open === s.key}
                <ul class="zones small">
                  {#each s.runs as r (r.zone_id)}
                    <li class="row spread">
                      <span class="ellipsis grow">{zoneName(cache.zones, r.zone_id)}</span>
                      {#if r.skip}
                        <span class="muted">{t(('outcome_' + r.skip) as Key)}</span>
                      {:else}
                        <span class="tnum muted">{clock(r.est_start, tz)}</span>
                        <span class="tnum" style="min-width:4.5em;text-align:right">{dur(r.seconds)}</span>
                      {/if}
                    </li>
                  {/each}
                </ul>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  {/each}
{/if}

<style>
  .track {
    position: relative;
    height: 26px;
    border-radius: 6px;
    background: var(--bg);
    margin-top: 12px;
  }
  .tick {
    position: absolute;
    top: 0;
    bottom: 0;
    border-left: 1px dashed var(--line);
  }
  .tick span {
    position: absolute;
    top: -15px;
    left: -6px;
    font-size: 0.7rem;
    font-style: normal;
    color: var(--muted);
  }
  .seg-bar {
    position: absolute;
    top: 3px;
    bottom: 3px;
    min-width: 4px;
    min-height: 0;
    padding: 0;
    border: 0;
    border-radius: 4px;
    cursor: pointer;
  }
  .seg-bar.skipped {
    opacity: 0.35;
  }
  .seg-bar.overlap {
    outline: 2px solid var(--warn);
    outline-offset: 1px;
  }
  .dot {
    flex: none;
    width: 12px;
    height: 12px;
    border-radius: 50%;
  }
  .plain {
    all: unset;
    cursor: pointer;
    min-width: 0;
  }
  .plain:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .strike {
    text-decoration: line-through;
  }
  .warn-text {
    color: var(--warn);
  }
  li.sel {
    background: var(--accent-soft);
    border-radius: 10px;
  }
  .zones {
    list-style: none;
    margin: 6px 0 2px 22px;
    padding: 0;
  }
  .zones li {
    padding: 3px 0;
  }
</style>
