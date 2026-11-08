<script lang="ts">
  import { onMount } from 'svelte'
  import { api, live, cache, type RunView } from '../lib/api.svelte'
  import { t } from '../lib/i18n.svelte'
  import { dur, clock, when, zoneName, programName, minutes } from '../lib/format'
  import { toast, errMsg } from '../lib/ui.svelte'
  import { install, promptInstall } from '../lib/install.svelte'
  import Icon from '../lib/Icon.svelte'

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
  const maxMinutes = $derived(cache.zones.find((z) => z.id === pickZone)?.max_minutes ?? 120)
  const runningZones = $derived(new Set((s?.runner.running || []).map((r) => r.zone_id)))

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

  function step(d: number) {
    const v = Math.round(Number(pickMinutes) || 0) + d
    pickMinutes = Math.min(maxMinutes, Math.max(1, v))
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
    <span class="chip {live.connected ? 'ok' : 'warn'} small"
      ><Icon name={live.connected ? 'wifi' : 'wifioff'} />{live.connected ? t('live') : t('offline')}</span
    >
  </div>
</header>

{#if !s}
  <p class="muted">{t('loading')}</p>
{:else}
  {#if s.runner.fault}
    <div class="banner bad" role="alert">
      <Icon name="alert" />
      <div class="grow">
        <strong>{t('fault')}</strong>
        <div class="small">{s.runner.fault}</div>
        <button class="danger" style="margin-top:8px" onclick={() => act(() => api('POST', '/api/fault/clear', {}))}
          ><Icon name="check" />{t('clear_fault')}</button
        >
      </div>
    </div>
  {/if}
  {#if !s.controller_on}<div class="banner warn"><Icon name="alert" />{t('controller_off')}</div>{/if}
  {#if s.runner.driver === 'sim'}<div class="banner info"><Icon name="chip" />{t('driver_sim')}</div>{/if}

  <div class="row wrap-row" style="margin-bottom:14px">
    <span class="chip water" title={t('wl_manual')}
      ><Icon name="droplet" />{t('watering_level')} {s.watering_level}%</span
    >
    <button class="chip {s.rain_delay_until ? 'warn' : ''}" onclick={() => rainDialog.showModal()}>
      <Icon name="rain" />
      {s.rain_delay_until
        ? t('rain_delay_until', { t: when(s.rain_delay_until, tz, new Date(serverNow), true) })
        : t('no_rain_delay')}
    </button>
    {#if s.sensor.enabled}
      <span class="chip {s.sensor.wet ? 'warn' : 'ok'}"
        ><Icon name="sensor" />{s.sensor.wet ? t('sensor_wet') : t('sensor_dry')}</span
      >
    {/if}
    {#if s.sun}
      <span class="chip"><Icon name="sun" />{clock(s.sun.sunrise, tz)} – {clock(s.sun.sunset, tz)}</span>
    {/if}
  </div>

  <div class="row" style="gap:10px">
    <button class="primary big" onclick={() => openRun()} disabled={!runnable.length}
      ><Icon name="play" />{t('run_zone')}</button
    >
    <button class="danger big" onclick={stopAll} disabled={busy}><Icon name="stop" />{t('stop_all')}</button>
  </div>

  <h2><Icon name="droplet" />{t('running_now')}</h2>
  {#if s.runner.paused}
    <div class="banner warn row spread">
      <Icon name="pause" /><span class="grow">{t('paused')}</span>
      <button onclick={() => act(() => api('POST', '/api/resume', {}))}><Icon name="play" />{t('resume')}</button>
    </div>
  {/if}
  {#if s.runner.running.length === 0}
    <div class="card muted">{t('nothing_running')}</div>
  {:else}
    {#each s.runner.running as r (r.run_id)}
      {@const rem = left(r)}
      <div class="card running">
        <div class="row spread">
          <span class="zone-num on">{(cache.zones.find((z) => z.id === r.zone_id)?.output_index ?? -1) + 1 || '?'}</span
          >
          <div class="grow">
            <div class="ellipsis" style="font-weight:600">{zoneName(cache.zones, r.zone_id)}</div>
            <div class="small muted ellipsis">{programName(cache.programs, r.program_id)} · {dur(r.seconds)}</div>
          </div>
          <div class="tnum" style="font-size:1.5rem;font-weight:700">{dur(rem)}</div>
          <button
            class="icon-btn lg round danger"
            aria-label={t('stop')}
            title={t('stop')}
            onclick={() => act(() => api('POST', '/api/stop', { run_id: r.run_id }))}><Icon name="stop" /></button
          >
        </div>
        <div class="bar"><i style="width:{100 - (rem / r.seconds) * 100}%"></i></div>
      </div>
    {/each}
    {#if !s.runner.paused}
      <div class="row" style="margin-top:10px">
        <button onclick={() => act(() => api('POST', '/api/pause', {}))}><Icon name="pause" />{t('pause')}</button>
      </div>
    {/if}
  {/if}

  {#if s.runner.queue.length}
    <h2><Icon name="list" />{t('queued')}</h2>
    <div class="card">
      <ul class="list">
        {#each s.runner.queue as r (r.run_id)}
          <li class="row spread">
            <div class="grow">
              <div class="ellipsis">{zoneName(cache.zones, r.zone_id)}</div>
              <div class="small muted ellipsis">{programName(cache.programs, r.program_id)} · {dur(r.remaining)}</div>
            </div>
            <span class="tnum small muted">{clock(r.est_start, tz)}</span>
            <button
              class="icon-btn ghost"
              aria-label={t('stop')}
              title={t('stop')}
              onclick={() => act(() => api('POST', '/api/stop', { run_id: r.run_id }))}><Icon name="x" /></button
            >
          </li>
        {/each}
      </ul>
    </div>
  {/if}

  <h2><Icon name="clock" />{t('next_runs')}</h2>
  <div class="card">
    {#if s.next.length === 0}
      <span class="muted">{t('no_next')}</span>
    {:else}
      <ul class="list">
        {#each s.next as n}
          <li class="row spread">
            <div class="grow">
              <div class="ellipsis">{zoneName(cache.zones, n.zone_id)}</div>
              <div class="small muted ellipsis">
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
    <h2><Icon name="sensor" />{t('sim_sensor')}</h2>
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
    <div class="row spread">
      <h3>{t('run_zone')}</h3>
      <button type="button" class="icon-btn ghost" aria-label={t('cancel')} onclick={() => runDialog.close()}
        ><Icon name="x" /></button
      >
    </div>
    <div class="tiles" role="group" aria-label={t('zone')}>
      {#each runnable as z (z.id)}
        <button type="button" class="tile" aria-pressed={pickZone === z.id} onclick={() => (pickZone = z.id)}>
          <span class="row" style="gap:6px"
            ><Icon name="droplet" size={18} /><span class="num">{z.output_index + 1}</span>
            {#if runningZones.has(z.id)}<span class="live-dot" title={t('running_now')}></span>{/if}</span
          >
          <span class="name">{z.name}</span>
        </button>
      {/each}
    </div>
    <div>
      <div class="small muted" style="margin-bottom:6px">{t('minutes')}</div>
      <div class="stepper">
        <button type="button" class="icon-btn lg round" aria-label="−1" onclick={() => step(-1)}
          ><Icon name="minus" /></button
        >
        <input
          type="number"
          inputmode="decimal"
          min="0.1"
          max={maxMinutes}
          step="any"
          bind:value={pickMinutes}
          required
          aria-label={t('minutes')}
        />
        <button type="button" class="icon-btn lg round" aria-label="+1" onclick={() => step(1)}
          ><Icon name="plus" /></button
        >
      </div>
    </div>
    <div class="row wrap-row">
      {#each [1, 5, 10, 20, 30] as m}<button
          type="button"
          class="chip {pickMinutes === m ? 'ok' : ''}"
          onclick={() => (pickMinutes = m)}>{m} {t('min')}</button
        >{/each}
    </div>
    <button class="primary big" style="width:100%" disabled={busy || !pickZone}><Icon name="play" />{t('start')}</button
    >
  </form>
</dialog>

<dialog bind:this={rainDialog}>
  <div class="stack">
    <div class="row spread">
      <h3 class="row" style="gap:8px"><Icon name="rain" />{t('set_rain_delay')}</h3>
      <button type="button" class="icon-btn ghost" aria-label={t('cancel')} onclick={() => rainDialog.close()}
        ><Icon name="x" /></button
      >
    </div>
    <div class="grid2" style="grid-template-columns:1fr 1fr">
      {#each [12, 24, 48, 72] as h}<button style="min-height:56px" onclick={() => rainDelay(h)}>{h} {t('hours')}</button
        >{/each}
    </div>
    {#if s?.rain_delay_until}<button style="width:100%" onclick={() => rainDelay(0)}
        ><Icon name="x" />{t('clear')}</button
      >{/if}
  </div>
</dialog>

<style>
  .live-dot {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: var(--water);
  }
</style>
