<script lang="ts">
  import { onMount } from 'svelte'
  import { api, cache, loadZones, live, type Zone, type RunView, type Settings } from '../lib/api.svelte'
  import { t } from '../lib/i18n.svelte'
  import { dur } from '../lib/format'
  import { toast, errMsg } from '../lib/ui.svelte'

  let editing = $state<Zone | null>(null)
  let dialog: HTMLDialogElement
  let outputs = $state(8)
  let maxOpen = $state(0)

  let now = $state(Date.now())
  onMount(() => {
    loadZones()
    api<{ outputs: number }>('GET', '/api/info')
      .then((i) => (outputs = i.outputs))
      .catch(() => {})
    api<Settings>('GET', '/api/settings')
      .then((s) => (maxOpen = s.max_open_zones))
      .catch(() => {})
    const id = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(id)
  })

  // The run (running, waiting or queued) of each zone, from the live state.
  const runs = $derived.by(() => {
    // eslint-disable-next-line svelte/prefer-svelte-reactivity -- rebuilt by $derived, never mutated after
    const m = new Map<number, RunView>()
    const r = live.status?.runner
    for (const v of [...(r?.running || []), ...(r?.queue || [])]) if (!m.has(v.zone_id)) m.set(v.zone_id, v)
    return m
  })
  const openCount = $derived(live.status?.runner.running.length ?? 0)

  function left(r: RunView) {
    if (r.state !== 'running') return r.remaining
    return Math.max(0, (Date.parse(r.est_end) - (now + live.clockSkewMs)) / 1000)
  }

  async function stop(r: RunView) {
    try {
      await api('POST', '/api/stop', { run_id: r.run_id })
    } catch (e) {
      toast(errMsg(e), true)
    }
  }

  function open(z?: Zone) {
    const used = new Set(cache.zones.map((x) => x.output_index))
    let free = 0
    while (used.has(free)) free++
    editing = z
      ? { ...z }
      : {
          id: 0,
          output_index: free,
          name: '',
          enabled: true,
          group_id: 1,
          uses_master: false,
          ignore_rain: false,
          ignore_sensor: false,
          max_minutes: 120,
          sort_order: free,
        }
    dialog.showModal()
  }

  async function save(e: Event) {
    e.preventDefault()
    if (!editing) return
    try {
      const z = {
        ...editing,
        output_index: Number(editing.output_index),
        group_id: Number(editing.group_id),
        max_minutes: Number(editing.max_minutes),
      }
      if (z.id) await api('PUT', `/api/zones/${z.id}`, z)
      else await api('POST', '/api/zones', z)
      await loadZones()
      dialog.close()
      toast(t('saved'))
    } catch (err) {
      toast(errMsg(err), true)
    }
  }

  async function test(z: Zone) {
    try {
      await api('POST', '/api/run/zone', { zone_id: z.id, seconds: 60 })
      toast(`${z.name}: ${t('test_1min')}`)
    } catch (e) {
      toast(errMsg(e), true)
    }
  }
</script>

<header class="top">
  <h1>{t('zones')}</h1>
  {#if maxOpen}<span class="chip {openCount ? 'water' : ''} small">{t('open_of', { n: openCount, max: maxOpen })}</span
    >{/if}
  {#if cache.zones.length < outputs}<button onclick={() => open()}>+ {t('new_zone')}</button>{/if}
</header>

<div class="card">
  <ul class="list">
    {#each cache.zones as z (z.id)}
      {@const r = runs.get(z.id)}
      {@const rem = r ? left(r) : 0}
      <li>
        <div class="row">
          <span class="chip {r?.state === 'running' ? 'water' : ''} tnum" style="min-width:2.4em;justify-content:center"
            >{z.output_index + 1}</span
          >
          <button
            class="ghost grow"
            style="justify-content:flex-start;text-align:left;padding:4px"
            onclick={() => open(z)}
          >
            <span class="grow">
              <span class:muted={!z.enabled} style="font-weight:600">{z.name}</span>
              <span class="small muted" style="display:block">
                {z.enabled ? '' : t('disabled') + ' · '}{z.group_id
                  ? `${t('group')} ${z.group_id}`
                  : t('group_hint').split('=')[1]?.trim()}{z.ignore_rain ? ' · ' + t('ignore_rain') : ''}{z.uses_master
                  ? ' · ' + t('uses_master')
                  : ''} · ≤ {z.max_minutes}
                {t('min')}
              </span>
            </span>
          </button>
          {#if r}
            {#if r.state === 'running'}
              <span class="tnum" style="font-weight:600">{dur(rem)}</span>
            {:else}
              <span class="chip warn small">{t('waiting_slot')}</span>
            {/if}
            <button class="danger small" onclick={() => stop(r)}>{t('stop')}</button>
          {:else}
            <button class="small" onclick={() => test(z)} disabled={!z.enabled}>{t('test_1min')}</button>
          {/if}
        </div>
        {#if r && r.state === 'running'}
          <div class="bar"><i style="width:{100 - (rem / r.seconds) * 100}%"></i></div>
        {/if}
      </li>
    {/each}
  </ul>
</div>

<dialog bind:this={dialog}>
  {#if editing}
    <form class="stack" onsubmit={save}>
      <h3>{editing.id ? editing.name : t('new_zone')}</h3>
      <label class="field"><span>{t('name')}</span><input bind:value={editing.name} required maxlength="40" /></label>
      <div class="grid2">
        <label class="field"
          ><span>{t('output')}</span>
          <select bind:value={editing.output_index}>
            {#each Array.from({ length: outputs }, (_, i) => i) as i}<option value={i}>{i + 1}</option>{/each}
          </select>
        </label>
        <label class="field"
          ><span>{t('max_minutes')}</span><input
            type="number"
            min="1"
            max="720"
            bind:value={editing.max_minutes}
            required
          /></label
        >
      </div>
      <label class="field"
        ><span>{t('group')} <small>({t('group_hint')})</small></span><input
          type="number"
          min="0"
          max="99"
          bind:value={editing.group_id}
        /></label
      >
      <label class="check"><input type="checkbox" bind:checked={editing.enabled} />{t('enabled')}</label>
      <label class="check"><input type="checkbox" bind:checked={editing.ignore_rain} />{t('ignore_rain')}</label>
      <label class="check"><input type="checkbox" bind:checked={editing.ignore_sensor} />{t('ignore_sensor')}</label>
      <label class="check"><input type="checkbox" bind:checked={editing.uses_master} />{t('uses_master')}</label>
      <div class="row">
        <button type="button" class="grow" onclick={() => dialog.close()}>{t('cancel')}</button>
        <button class="primary grow">{t('save')}</button>
      </div>
    </form>
  {/if}
</dialog>
