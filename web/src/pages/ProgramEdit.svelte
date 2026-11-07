<script lang="ts">
  import { onMount } from 'svelte'
  import { api, cache, loadPrograms, loadZones, live, type Program, type PlannedRun } from '../lib/api.svelte'
  import { t, type Key } from '../lib/i18n.svelte'
  import { WEEKDAYS, dur, when, zoneName, clock } from '../lib/format'
  import { toast, errMsg, go } from '../lib/ui.svelte'

  let { id }: { id: string } = $props()
  const isNew = $derived(id === 'new')
  const today = new Date().toLocaleDateString('en-CA')

  function blank(): Program {
    return {
      id: 0, name: '', enabled: true, weather_scaled: true, sort_order: 0, season_from: '', season_to: '',
      day_rule: { kind: 'weekdays', days: ['mon', 'wed', 'fri'], every: 2, anchor: today, parity: '' },
      start_rule: { kind: 'fixed', times: ['06:00'], first: '05:00', every_min: 60, count: 1, event: 'sunrise', offset_min: -30 },
      zones: [],
    }
  }

  let p = $state<Program>(blank())
  let loaded = $state(false)
  let saving = $state(false)
  let error = $state('')

  // Fixed start time rows, each a clock time or sunrise/sunset ± offset.
  type TimeRow = { mode: 'clock' | 'sunrise' | 'sunset'; clock: string; offset: number }
  let rows = $state<TimeRow[]>([{ mode: 'clock', clock: '06:00', offset: 0 }])

  function parseSpec(s: string): TimeRow {
    const m = /^(sunrise|sunset)([+-]\d+)?$/.exec(s)
    if (m) return { mode: m[1] as 'sunrise' | 'sunset', clock: '06:00', offset: m[2] ? Number(m[2]) : 0 }
    return { mode: 'clock', clock: s, offset: 0 }
  }
  function specOf(r: TimeRow): string {
    if (r.mode === 'clock') return r.clock
    return r.offset ? `${r.mode}${r.offset > 0 ? '+' : ''}${r.offset}` : r.mode
  }

  onMount(async () => {
    await loadZones()
    if (isNew) loaded = true
    else {
      await loadPrograms()
      const found = cache.programs.find((x) => x.id === Number(id))
      if (!found) { error = 'Not found'; return }
      const copy: Program = JSON.parse(JSON.stringify(found))
      const b = blank()
      copy.day_rule = { ...b.day_rule, ...copy.day_rule, parity: copy.day_rule.parity || '' }
      copy.start_rule = { ...b.start_rule, ...copy.start_rule }
      p = copy
      if (p.start_rule.kind === 'fixed') rows = (p.start_rule.times || []).map(parseSpec)
      if (p.start_rule.kind === 'repeat') repeatFirst = parseSpec(p.start_rule.first || '05:00')
      loaded = true
    }
  })

  let repeatFirst = $state<TimeRow>({ mode: 'clock', clock: '05:00', offset: 0 })

  // The program as the API wants it (only the fields of the chosen kinds).
  function payload(): Program {
    const d = p.day_rule
    const day_rule =
      d.kind === 'weekdays' ? { kind: d.kind, days: d.days, parity: d.parity || undefined, skip31: d.parity === 'odd' ? d.skip31 : undefined }
      : d.kind === 'interval' ? { kind: d.kind, every: Number(d.every), anchor: d.anchor, parity: d.parity || undefined, skip31: d.parity === 'odd' ? d.skip31 : undefined }
      : { kind: d.kind, parity: d.parity || 'odd', skip31: d.skip31 }
    const s = p.start_rule
    const start_rule =
      s.kind === 'fixed' ? { kind: s.kind, times: rows.map(specOf) }
      : s.kind === 'repeat' ? { kind: s.kind, first: specOf(repeatFirst), every_min: Number(s.every_min), count: Number(s.count) }
      : { kind: s.kind, event: s.event, offset_min: Number(s.offset_min) }
    return { ...p, name: p.name || 'preview', day_rule: day_rule as Program['day_rule'], start_rule: start_rule as Program['start_rule'] }
  }

  // Live preview of the next 5 starts, debounced.
  type Preview = { time: string; runs: PlannedRun[]; total_seconds: number }
  let preview = $state<Preview[] | null>(null)
  let previewErr = $state('')
  let timer: number | undefined
  $effect(() => {
    if (!loaded) return
    const body = JSON.stringify(payload()) // tracks every field
    clearTimeout(timer)
    timer = window.setTimeout(async () => {
      try {
        preview = await api<Preview[]>('POST', '/api/programs/preview', JSON.parse(body))
        previewErr = ''
      } catch (e) {
        previewErr = errMsg(e)
        preview = null
      }
    }, 300)
  })

  async function save() {
    saving = true
    error = ''
    try {
      const body = payload()
      body.name = p.name.trim()
      if (isNew) await api('POST', '/api/programs', body)
      else await api('PUT', `/api/programs/${p.id}`, body)
      await loadPrograms()
      toast(t('saved'))
      go('/programs')
    } catch (e) {
      error = errMsg(e)
    } finally {
      saving = false
    }
  }

  async function remove() {
    if (!confirm(t('delete_program'))) return
    try {
      await api('DELETE', `/api/programs/${p.id}`)
      await loadPrograms()
      go('/programs')
    } catch (e) { toast(errMsg(e), true) }
  }

  function toggleDay(d: string) {
    const days = p.day_rule.days || []
    p.day_rule.days = days.includes(d) ? days.filter((x) => x !== d) : [...days, d]
  }

  // Zone list with drag-to-order (pointer events work for touch and mouse)
  let dragIndex = $state<number | null>(null)
  function onPointerDown(i: number, e: PointerEvent) {
    dragIndex = i
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
  }
  function onPointerMove(e: PointerEvent) {
    if (dragIndex === null) return
    const el = document.elementFromPoint(e.clientX, e.clientY)?.closest('[data-zi]') as HTMLElement | null
    if (!el) return
    const to = Number(el.dataset.zi)
    if (to !== dragIndex) {
      const z = [...p.zones]
      const [moved] = z.splice(dragIndex, 1)
      z.splice(to, 0, moved)
      p.zones = z
      dragIndex = to
    }
  }
  function move(i: number, d: number) {
    const j = i + d
    if (j < 0 || j >= p.zones.length) return
    const z = [...p.zones]
    ;[z[i], z[j]] = [z[j], z[i]]
    p.zones = z
  }
  const freeZones = $derived(cache.zones.filter((z) => !p.zones.some((pz) => pz.zone_id === z.id)))
  let addZoneId = $state(0)
  function addZone() {
    const zid = addZoneId || freeZones[0]?.id
    if (!zid) return
    p.zones = [...p.zones, { zone_id: zid, seconds: 600 }]
    addZoneId = 0
  }

  const tz = $derived(live.status?.timezone || 'Europe/Sofia')
  const dayKinds: { k: Program['day_rule']['kind']; l: Key }[] = [{ k: 'weekdays', l: 'weekdays' }, { k: 'interval', l: 'interval' }, { k: 'oddeven', l: 'oddeven' }]
  const startKinds: { k: Program['start_rule']['kind']; l: Key }[] = [{ k: 'fixed', l: 'fixed' }, { k: 'repeat', l: 'repeat' }, { k: 'solar', l: 'solar' }]
</script>

{#snippet timeRow(r: TimeRow)}
  <select bind:value={r.mode} style="width:auto">
    <option value="clock">{t('clock')}</option>
    <option value="sunrise">{t('sunrise')}</option>
    <option value="sunset">{t('sunset')}</option>
  </select>
  {#if r.mode === 'clock'}
    <input type="time" bind:value={r.clock} required />
  {:else}
    <input type="number" bind:value={r.offset} min="-720" max="720" step="1" aria-label={t('offset_min')} />
    <span class="small muted">{t('min')} {r.offset < 0 ? t('before') : t('after')}</span>
  {/if}
{/snippet}

<header class="top">
  <button class="ghost" onclick={() => go('/programs')}>← {t('back')}</button>
  <h1 class="grow ellipsis">{isNew ? t('new_program') : p.name}</h1>
</header>

{#if !loaded}
  <p class="muted">{error || t('loading')}</p>
{:else}
  <form class="stack" onsubmit={(e) => { e.preventDefault(); save() }}>
    <div class="card stack">
      <label class="field"><span>{t('name')}</span><input bind:value={p.name} required maxlength="40" /></label>
      <label class="check"><input type="checkbox" bind:checked={p.enabled} />{t('enabled')}</label>
      <label class="check"><input type="checkbox" bind:checked={p.weather_scaled} />{t('weather_scaled')}</label>
    </div>

    <h2>{t('days')}</h2>
    <div class="card stack">
      <div class="seg" role="group">
        {#each dayKinds as k}
          <button type="button" aria-pressed={p.day_rule.kind === k.k} onclick={() => (p.day_rule.kind = k.k)}>{t(k.l)}</button>
        {/each}
      </div>
      {#if p.day_rule.kind === 'weekdays'}
        <div class="days">
          {#each WEEKDAYS as d}
            <button type="button" aria-pressed={(p.day_rule.days || []).includes(d)} onclick={() => toggleDay(d)}>{t(d)}</button>
          {/each}
        </div>
      {:else if p.day_rule.kind === 'interval'}
        <div class="row wrap-row">
          <span>{t('every')}</span><input type="number" min="1" max="365" bind:value={p.day_rule.every} required />
          <span>{t('days_unit')}, {t('starting')}</span><input type="date" bind:value={p.day_rule.anchor} required style="width:auto" />
        </div>
      {/if}
      <div class="row wrap-row">
        <span class="small muted">{t('only_dates')}</span>
        <div class="seg" role="group">
          {#if p.day_rule.kind !== 'oddeven'}
            <button type="button" aria-pressed={!p.day_rule.parity} onclick={() => (p.day_rule.parity = '')}>{t('any')}</button>
          {/if}
          <button type="button" aria-pressed={p.day_rule.parity === 'odd' || (p.day_rule.kind === 'oddeven' && !p.day_rule.parity)} onclick={() => (p.day_rule.parity = 'odd')}>{t('odd')}</button>
          <button type="button" aria-pressed={p.day_rule.parity === 'even'} onclick={() => (p.day_rule.parity = 'even')}>{t('even')}</button>
        </div>
      </div>
      {#if p.day_rule.parity === 'odd' || (p.day_rule.kind === 'oddeven' && !p.day_rule.parity)}
        <label class="check"><input type="checkbox" bind:checked={p.day_rule.skip31} />{t('skip31')}</label>
      {/if}
      <div class="small muted">{t('season')} — {t('season_hint')}</div>
      <div class="row wrap-row">
        <span>{t('from')}</span><input placeholder="MM-DD" pattern="\d\d-\d\d" bind:value={p.season_from} style="width:6em" />
        <span>{t('to')}</span><input placeholder="MM-DD" pattern="\d\d-\d\d" bind:value={p.season_to} style="width:6em" />
      </div>
    </div>

    <h2>{t('start_times')}</h2>
    <div class="card stack">
      <div class="seg" role="group">
        {#each startKinds as k}
          <button type="button" aria-pressed={p.start_rule.kind === k.k} onclick={() => (p.start_rule.kind = k.k)}>{t(k.l)}</button>
        {/each}
      </div>
      {#if p.start_rule.kind === 'fixed'}
        {#each rows as r, i}
          <div class="row wrap-row">
            {@render timeRow(r)}
            {#if rows.length > 1}<button type="button" class="icon-btn ghost" aria-label={t('delete')} onclick={() => (rows = rows.filter((_, j) => j !== i))}>✕</button>{/if}
          </div>
        {/each}
        {#if rows.length < 4}
          <div><button type="button" onclick={() => (rows = [...rows, { mode: 'clock', clock: '19:00', offset: 0 }])}>+ {t('add')}</button></div>
        {/if}
      {:else if p.start_rule.kind === 'repeat'}
        <div class="row wrap-row"><span>{t('first_start')}</span>{@render timeRow(repeatFirst)}</div>
        <div class="row wrap-row">
          <span>{t('repeat_every')}</span><input type="number" min="1" max="1440" bind:value={p.start_rule.every_min} required /><span>{t('min')},</span>
          <input type="number" min="0" max="1440" bind:value={p.start_rule.count} required /><span>{t('more_times')}</span>
        </div>
      {:else}
        <div class="row wrap-row">
          <select bind:value={p.start_rule.event} style="width:auto">
            <option value="sunrise">{t('sunrise')}</option><option value="sunset">{t('sunset')}</option>
          </select>
          <input type="number" min="-720" max="720" bind:value={p.start_rule.offset_min} aria-label={t('offset_min')} />
          <span class="small muted">{t('min')} {(p.start_rule.offset_min ?? 0) < 0 ? t('before') : t('after')}</span>
        </div>
      {/if}
    </div>

    <h2>{t('zone_order')}</h2>
    <div class="card">
      <ul class="list" onpointermove={onPointerMove} onpointerup={() => (dragIndex = null)} onpointercancel={() => (dragIndex = null)}>
        {#each p.zones as pz, i (pz.zone_id)}
          <li class="row" data-zi={i} class:dragging={dragIndex === i}>
            <span class="drag" onpointerdown={(e) => onPointerDown(i, e)} role="button" tabindex="-1" aria-label="drag">⠿</span>
            <span class="grow" style="overflow-wrap:anywhere">{zoneName(cache.zones, pz.zone_id)}</span>
            <input type="number" min="0.2" max="720" step="any" value={pz.seconds / 60} style="width:4.2em;padding:8px 6px"
              oninput={(e) => (pz.seconds = Math.round(Number((e.target as HTMLInputElement).value) * 60))} aria-label={t('minutes')} />
            <span class="small muted">{t('min')}</span>
            <button type="button" class="icon-btn ghost" style="padding:0 6px;min-width:34px" aria-label="up" onclick={() => move(i, -1)} disabled={i === 0}>↑</button>
            <button type="button" class="icon-btn ghost" style="padding:0 6px;min-width:34px" aria-label={t('delete')} onclick={() => (p.zones = p.zones.filter((_, j) => j !== i))}>✕</button>
          </li>
        {/each}
      </ul>
      {#if freeZones.length}
        <div class="row" style="margin-top:8px">
          <select value={addZoneId || freeZones[0]?.id} onchange={(e) => (addZoneId = Number((e.target as HTMLSelectElement).value))} class="grow">
            {#each freeZones as z}<option value={z.id}>{z.output_index + 1}. {z.name}{z.enabled ? '' : ` (${t('disabled')})`}</option>{/each}
          </select>
          <button type="button" onclick={addZone}>+ {t('add_zone')}</button>
        </div>
      {/if}
    </div>

    <h2>{t('preview')}</h2>
    <div class="card">
      {#if previewErr}
        <span class="chip bad">{previewErr}</span>
      {:else if preview && preview.length === 0}
        <span class="muted">{t('no_starts')}</span>
      {:else if preview}
        <ul class="list">
          {#each preview as s}
            <li>
              <div class="row spread"><strong class="tnum">{when(s.time, tz)}</strong><span class="small muted">{dur(s.total_seconds)} {t('total')}</span></div>
              <div class="small muted">
                {#each s.runs as r, i}{i ? ' → ' : ''}<span class:muted={!!r.skip}>{zoneName(cache.zones, r.zone_id)} {clock(r.est_start, tz)} ({dur(r.seconds)})</span>{/each}
              </div>
            </li>
          {/each}
        </ul>
      {:else}
        <span class="muted">{t('loading')}</span>
      {/if}
    </div>

    {#if error}<div class="banner bad">{error}</div>{/if}
    <div class="row">
      {#if !isNew}<button type="button" class="ghost" onclick={remove}>{t('delete')}</button>{/if}
      <span class="grow"></span>
      <button type="button" onclick={() => go('/programs')}>{t('cancel')}</button>
      <button class="primary" disabled={saving}>{t('save')}</button>
    </div>
  </form>
{/if}
