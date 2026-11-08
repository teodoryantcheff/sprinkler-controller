<script lang="ts">
  import { onMount } from 'svelte'
  import { api, live, cache, type RunView } from '../lib/api.svelte'
  import { t } from '../lib/i18n.svelte'
  import { dur, clock, when, zoneName, programName, minutes } from '../lib/format'
  import { toast, errMsg } from '../lib/ui.svelte'
  import { install, promptInstall } from '../lib/install.svelte'

  const s = $derived(live.status)
  const tz = $derived(s?.timezone || 'Europe/Sofia')
  let now = $state(Date.now())
  onMount(() => {
    const id = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(id)
  })
  const serverNow = $derived(now + live.clockSkewMs)

  function left(r: RunView) {
    if (r.state !== 'running') return r.remaining
    return Math.max(0, (Date.parse(r.est_end) - serverNow) / 1000)
  }

  let runDialog: HTMLDialogElement
  let rainDialog: HTMLDialogElement
  let pickZone = $state(0)
  let pickMinutes = $state(10)
  let busy = $state(false)

  const runnable = $derived(cache.zones.filter((z) => z.enabled))

  async function act(fn: () => Promise<unknown>, ok?: string) {
    busy = true
    try {
      await fn()
      if (ok) toast(ok)
    } catch (e) {
      toast(errMsg(e), true)
    } finally {
      busy = false
    }
  }

  function openRun(zoneId?: number) {
    pickZone = zoneId ?? pickZone ?? runnable[0]?.id
    if (!pickZone && runnable.length) pickZone = runnable[0].id
    runDialog.showModal()
  }

  async function startRun(e: Event) {
    e.preventDefault()
    const name = zoneName(cache.zones, pickZone)
    await act(
      () => api('POST', '/api/run/zone', { zone_id: pickZone, seconds: Math.round(pickMinutes * 60) }),
      `${name}: ${minutes(pickMinutes * 60)}`,
    )
    runDialog.close()
  }

  async function stopAll() {
    if (!confirm(t('confirm_stop_all'))) return
    await act(() => api('POST', '/api/stop', {}), t('stop_all'))
  }

  async function rainDelay(hours: number) {
    await act(() => api('POST', '/api/raindelay', { hours }))
    rainDialog.close()
  }
</script>

<header class="top">
  <h1>{t('home')}</h1>
  <div class="row">
    {#if install.canPrompt && !install.installed}<button class="small" onclick={promptInstall}
        >{t('install_app')}</button
      >{/if}
    <span class="chip {live.connected ? 'ok' : 'warn'} small">{live.connected ? t('live') : t('offline')}</span>
  </div>
</header>

{#if !s}
  <p class="muted">{t('loading')}</p>
{:else}
  {#if s.runner.fault}
    <div class="banner bad" role="alert">
      <strong>{t('fault')}</strong>
      <div class="small">{s.runner.fault}</div>
      <button class="danger" style="margin-top:8px" onclick={() => act(() => api('POST', '/api/fault/clear', {}))}
        >{t('clear_fault')}</button
      >
    </div>
  {/if}
  {#if !s.controller_on}<div class="banner warn">{t('controller_off')}</div>{/if}
  {#if s.runner.driver === 'sim'}<div class="banner info">{t('driver_sim')}</div>{/if}

  <div class="row wrap-row" style="margin-bottom:12px">
    <span class="chip water" title={t('wl_manual')}>{t('watering_level')} {s.watering_level}%</span>
    <button class="chip {s.rain_delay_until ? 'warn' : ''}" onclick={() => rainDialog.showModal()}>
      {s.rain_delay_until ? t('rain_delay_until', { t: when(s.rain_delay_until, tz) }) : t('no_rain_delay')}
    </button>
    {#if s.sensor.enabled}
      <span class="chip {s.sensor.wet ? 'warn' : 'ok'}">{s.sensor.wet ? t('sensor_wet') : t('sensor_dry')}</span>
    {/if}
    {#if s.sun}
      <span class="chip small">☀ {clock(s.sun.sunrise, tz)} – {clock(s.sun.sunset, tz)}</span>
    {/if}
  </div>

  <div class="row" style="gap:10px">
    <button class="primary big" onclick={() => openRun()} disabled={!runnable.length}>{t('run_zone')}</button>
    <button class="danger big" onclick={stopAll} disabled={busy}>{t('stop_all')}</button>
  </div>

  <h2>{t('running_now')}</h2>
  {#if s.runner.paused}
    <div class="banner warn row spread">
      <span>{t('paused')}</span>
      <button onclick={() => act(() => api('POST', '/api/resume', {}))}>{t('resume')}</button>
    </div>
  {/if}
  {#if s.runner.running.length === 0}
    <div class="card muted">{t('nothing_running')}</div>
  {:else}
    {#each s.runner.running as r (r.run_id)}
      {@const rem = left(r)}
      <div class="card running">
        <div class="row spread">
          <div class="grow">
            <div style="font-weight:600">{zoneName(cache.zones, r.zone_id)}</div>
            <div class="small muted">{programName(cache.programs, r.program_id)} · {dur(r.seconds)}</div>
          </div>
          <div class="tnum" style="font-size:1.4rem;font-weight:600">{dur(rem)}</div>
          <button onclick={() => act(() => api('POST', '/api/stop', { run_id: r.run_id }))}>{t('stop')}</button>
        </div>
        <div class="bar"><i style="width:{100 - (rem / r.seconds) * 100}%"></i></div>
      </div>
    {/each}
    {#if !s.runner.paused}
      <div class="row" style="margin-top:8px">
        <button class="small" onclick={() => act(() => api('POST', '/api/pause', {}))}>{t('pause')}</button>
      </div>
    {/if}
  {/if}

  {#if s.runner.queue.length}
    <h2>{t('queued')}</h2>
    <div class="card">
      <ul class="list">
        {#each s.runner.queue as r (r.run_id)}
          <li class="row spread">
            <div class="grow">
              <div>{zoneName(cache.zones, r.zone_id)}</div>
              <div class="small muted">{programName(cache.programs, r.program_id)} · {dur(r.remaining)}</div>
            </div>
            <span class="tnum small muted">{clock(r.est_start, tz)}</span>
            <button
              class="icon-btn ghost"
              aria-label={t('stop')}
              onclick={() => act(() => api('POST', '/api/stop', { run_id: r.run_id }))}>✕</button
            >
          </li>
        {/each}
      </ul>
    </div>
  {/if}

  <h2>{t('next_runs')}</h2>
  <div class="card">
    {#if s.next.length === 0}
      <span class="muted">{t('no_next')}</span>
    {:else}
      <ul class="list">
        {#each s.next as n}
          <li class="row spread">
            <div class="grow">
              <div>{zoneName(cache.zones, n.zone_id)}</div>
              <div class="small muted">
                {programName(cache.programs, n.program_id)}{n.scale_pct !== 100 ? ` · ${n.scale_pct}%` : ''}
              </div>
            </div>
            <div style="text-align:right">
              <div class="tnum">{when(n.est_start, tz, new Date(serverNow))}</div>
              <div class="small muted">{dur(n.seconds)}</div>
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  {#if s.sensor.sim && s.sensor.enabled}
    <h2>{t('sim_sensor')}</h2>
    <div class="card row spread">
      <span>{s.sensor.raw ? t('sensor_wet') : t('sensor_dry')}</span>
      <button onclick={() => act(() => api('POST', '/api/sim/sensor', { wet: !s.sensor.raw }))}
        >{s.sensor.raw ? t('make_dry') : t('make_wet')}</button
      >
    </div>
  {/if}
{/if}

<dialog bind:this={runDialog}>
  <form class="stack" onsubmit={startRun}>
    <h3>{t('run_zone')}</h3>
    <label class="field"
      ><span>{t('zone')}</span>
      <select bind:value={pickZone}>
        {#each runnable as z}<option value={z.id}>{z.output_index + 1}. {z.name}</option>{/each}
      </select>
    </label>
    <label class="field"
      ><span>{t('minutes')}</span>
      <input
        type="number"
        min="0.1"
        max={cache.zones.find((z) => z.id === pickZone)?.max_minutes ?? 120}
        step="any"
        bind:value={pickMinutes}
        required
      />
    </label>
    <div class="row wrap-row">
      {#each [1, 5, 10, 20, 30] as m}<button type="button" class="chip" onclick={() => (pickMinutes = m)}
          >{m} {t('min')}</button
        >{/each}
    </div>
    <div class="row">
      <button type="button" class="grow" onclick={() => runDialog.close()}>{t('cancel')}</button>
      <button class="primary grow" disabled={busy}>{t('start')}</button>
    </div>
  </form>
</dialog>

<dialog bind:this={rainDialog}>
  <div class="stack">
    <h3>{t('set_rain_delay')}</h3>
    <div class="row wrap-row">
      {#each [12, 24, 48, 72] as h}<button onclick={() => rainDelay(h)}>{h} {t('hours')}</button>{/each}
    </div>
    <div class="row">
      <button class="grow" onclick={() => rainDialog.close()}>{t('cancel')}</button>
      {#if s?.rain_delay_until}<button class="grow" onclick={() => rainDelay(0)}>{t('clear')}</button>{/if}
    </div>
  </div>
</dialog>
