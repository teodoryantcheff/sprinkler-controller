<script lang="ts">
  import { onMount } from 'svelte'
  import { api, cache, loadZones, session, stopLive, type Settings } from '../lib/api.svelte'
  import { t, setLang } from '../lib/i18n.svelte'
  import { toast, errMsg, theme, applyTheme, type Theme } from '../lib/ui.svelte'
  import { install, promptInstall } from '../lib/install.svelte'
  import Icon from '../lib/Icon.svelte'

  let s = $state<Settings | null>(null)
  let info = $state<Record<string, string | number>>({})
  let saving = $state(false)
  let curPw = $state('')
  let newPw = $state('')
  const zones = $derived(cache.zones)
  const timezones: string[] = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.(
    'timeZone',
  ) ?? ['Europe/Sofia', 'UTC']

  onMount(async () => {
    loadZones()
    try {
      s = await api<Settings>('GET', '/api/settings')
      info = await api('GET', '/api/info')
    } catch (e) {
      toast(errMsg(e), true)
    }
  })

  async function save(e: Event) {
    e.preventDefault()
    if (!s) return
    saving = true
    try {
      const num = (v: unknown) => Number(v)
      s = await api<Settings>('PUT', '/api/settings', {
        ...s,
        lat: num(s.lat),
        lon: num(s.lon),
        station_delay_s: num(s.station_delay_s),
        master_zone_id: num(s.master_zone_id),
        master_on_delay_s: num(s.master_on_delay_s),
        master_off_delay_s: num(s.master_off_delay_s),
        watering_level_pct: num(s.watering_level_pct),
        max_open_zones: num(s.max_open_zones),
        daily_cap_minutes: num(s.daily_cap_minutes),
        sensor: { ...s.sensor, on_delay_s: num(s.sensor.on_delay_s), off_delay_s: num(s.sensor.off_delay_s) },
      })
      setLang(s!.language)
      session.deviceName = s!.device_name
      toast(t('saved'))
    } catch (err) {
      toast(errMsg(err), true)
    } finally {
      saving = false
    }
  }

  function locate() {
    navigator.geolocation?.getCurrentPosition(
      (p) => {
        if (s) {
          s.lat = Math.round(p.coords.latitude * 1e5) / 1e5
          s.lon = Math.round(p.coords.longitude * 1e5) / 1e5
        }
      },
      () => toast(t('location_failed'), true),
    )
  }

  async function changePw(e: Event) {
    e.preventDefault()
    try {
      await api('POST', '/api/password', { current: curPw, new: newPw })
      curPw = newPw = ''
      toast(t('saved'))
    } catch (err) {
      toast(errMsg(err), true)
    }
  }

  async function restore(e: Event) {
    const f = (e.target as HTMLInputElement).files?.[0]
    if (!f || !confirm(t('restore_confirm'))) return
    try {
      await api('POST', '/api/restore', JSON.parse(await f.text()))
      toast(t('restored'))
      location.reload()
    } catch (err) {
      toast(errMsg(err), true)
    }
  }

  async function logout() {
    await api('POST', '/api/logout', {}).catch(() => {})
    stopLive()
    session.loggedIn = false
  }
</script>

<header class="top">
  <h1>{t('settings')}</h1>
  <button class="ghost" onclick={logout}><Icon name="logout" />{t('logout')}</button>
</header>

{#if !s}
  <p class="muted">{t('loading')}</p>
{:else}
  <form onsubmit={save} class="stack">
    <h2><Icon name="chip" />{t('device')}</h2>
    <div class="card stack">
      <label class="field"><span>{t('device_name')}</span><input bind:value={s.device_name} maxlength="40" /></label>
      <div class="grid2">
        <label class="field"
          ><span>{t('language')}</span>
          <select bind:value={s.language}
            ><option value="en">English</option><option value="bg">Български</option></select
          >
        </label>
        <label class="field"
          ><span>{t('theme')}</span>
          <select value={theme.value} onchange={(e) => applyTheme((e.target as HTMLSelectElement).value as Theme)}>
            <option value="auto">{t('theme_auto')}</option><option value="light">{t('theme_light')}</option><option
              value="dark">{t('theme_dark')}</option
            >
          </select>
        </label>
      </div>
      <label class="check"><input type="checkbox" bind:checked={s.controller_on} />{t('controller_on')}</label>
    </div>

    <h2><Icon name="pin" />{t('location')}</h2>
    <div class="card stack">
      <div class="grid2">
        <label class="field"
          ><span>{t('latitude')}</span><input
            type="number"
            step="any"
            min="-90"
            max="90"
            bind:value={s.lat}
            style="width:100%"
          /></label
        >
        <label class="field"
          ><span>{t('longitude')}</span><input
            type="number"
            step="any"
            min="-180"
            max="180"
            bind:value={s.lon}
            style="width:100%"
          /></label
        >
      </div>
      <div><button type="button" onclick={locate}><Icon name="locate" />{t('use_my_location')}</button></div>
      <label class="field"
        ><span>{t('timezone')}</span>
        <select bind:value={s.timezone}
          >{#each timezones as z}<option>{z}</option>{/each}</select
        >
      </label>
    </div>

    <h2><Icon name="droplet" />{t('watering')}</h2>
    <div class="card stack">
      <div class="grid2">
        <label class="field"
          ><span>{t('watering_level')} (%)</span><input
            type="number"
            min="0"
            max="250"
            bind:value={s.watering_level_pct}
            style="width:100%"
          /></label
        >
        <label class="field"
          ><span>{t('zone_delay')}</span><input
            type="number"
            min="0"
            max="600"
            bind:value={s.station_delay_s}
            style="width:100%"
          /></label
        >
      </div>
      <label class="field"
        ><span>{t('master')}</span>
        <select bind:value={s.master_zone_id}>
          <option value={0}>{t('none')}</option>
          {#each zones as z}<option value={z.id}>{z.output_index + 1}. {z.name}</option>{/each}
        </select>
      </label>
      {#if s.master_zone_id}
        <div class="grid2">
          <label class="field"
            ><span>{t('on_delay')}</span><input
              type="number"
              min="0"
              max="600"
              bind:value={s.master_on_delay_s}
              style="width:100%"
            /></label
          >
          <label class="field"
            ><span>{t('off_delay')}</span><input
              type="number"
              min="0"
              max="600"
              bind:value={s.master_off_delay_s}
              style="width:100%"
            /></label
          >
        </div>
      {/if}
    </div>

    <h2><Icon name="sensor" />{t('sensor_settings')}</h2>
    <div class="card stack">
      <label class="check"><input type="checkbox" bind:checked={s.sensor.enabled} />{t('sensor_enabled')}</label>
      {#if s.sensor.enabled}
        <div class="grid2">
          <label class="field"
            ><span>{t('kind')}</span>
            <select bind:value={s.sensor.kind}
              ><option value="rain">{t('rain')}</option><option value="soil">{t('soil')}</option></select
            >
          </label>
          <label class="field"
            ><span>{t('normally')}</span>
            <select bind:value={s.sensor.normally}
              ><option value="open">{t('normally_open')}</option><option value="closed">{t('normally_closed')}</option
              ></select
            >
          </label>
          <label class="field"
            ><span>{t('sensor_on_delay')}</span><input
              type="number"
              min="0"
              max="86400"
              bind:value={s.sensor.on_delay_s}
              style="width:100%"
            /></label
          >
          <label class="field"
            ><span>{t('sensor_off_delay')}</span><input
              type="number"
              min="0"
              max="86400"
              bind:value={s.sensor.off_delay_s}
              style="width:100%"
            /></label
          >
        </div>
      {/if}
    </div>

    <h2><Icon name="shield" />{t('safety')}</h2>
    <div class="card stack">
      <div class="grid2">
        <label class="field"
          ><span>{t('max_open')}</span><input
            type="number"
            min="1"
            max="16"
            bind:value={s.max_open_zones}
            style="width:100%"
          /></label
        >
        <label class="field"
          ><span>{t('daily_cap')}</span><input
            type="number"
            min="1"
            max="1440"
            bind:value={s.daily_cap_minutes}
            style="width:100%"
          /></label
        >
      </div>
    </div>

    <h2><Icon name="globe" />{t('access')}</h2>
    <div class="card stack">
      <label class="check"><input type="checkbox" bind:checked={s.lan_no_login} />{t('lan_no_login')}</label>
    </div>

    <div class="row savebar">
      <button class="primary grow" disabled={saving}><Icon name="check" />{t('save')}</button>
    </div>
  </form>

  <h2><Icon name="download" />{t('app')}</h2>
  <div class="card stack small">
    {#if install.installed}
      <span class="chip ok">{t('installed')}</span>
    {:else}
      {#if install.canPrompt}
        <div><button class="primary" onclick={promptInstall}>{t('install_app')}</button></div>
      {:else if install.ios}
        <div>{t('ios_steps')}</div>
      {:else}
        <div>{t('android_steps')}</div>
      {/if}
    {/if}
  </div>

  <h2><Icon name="key" />{t('change_password')}</h2>
  <form class="card stack" onsubmit={changePw}>
    <label class="field"
      ><span>{t('current_password')}</span><input
        type="password"
        bind:value={curPw}
        autocomplete="current-password"
        required
      /></label
    >
    <label class="field"
      ><span>{t('new_password')}</span><input
        type="password"
        bind:value={newPw}
        autocomplete="new-password"
        minlength="7"
        required
      /></label
    >
    <div><button>{t('change_password')}</button></div>
  </form>

  <h2><Icon name="download" />{t('backup')}</h2>
  <div class="card row wrap-row">
    <a class="btn grow" href="/api/backup" download><Icon name="download" />{t('download_backup')}</a>
    <label class="btn grow"
      ><Icon name="upload" />{t('restore')}<input
        type="file"
        accept="application/json,.json"
        onchange={restore}
        hidden
      /></label
    >
  </div>

  <h2><Icon name="chip" />{t('hardware')}</h2>
  <div class="card small">
    <div>{t('driver')}: <strong>{info.driver}</strong> · {t('outputs')}: {info.outputs}</div>
    <div class="muted">{t('version')} {info.version} · {info.hostname} · {info.arch} · {info.go}</div>
    <div><a href="/api/docs" target="_blank" rel="noopener">{t('api_docs')}</a></div>
  </div>
{/if}
