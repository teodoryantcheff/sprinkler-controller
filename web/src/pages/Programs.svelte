<script lang="ts">
  import { onMount } from 'svelte'
  import { api, cache, loadPrograms, loadZones, type Program } from '../lib/api.svelte'
  import { t } from '../lib/i18n.svelte'
  import { programSummary } from '../lib/format'
  import { toast, errMsg, go } from '../lib/ui.svelte'
  import Icon from '../lib/Icon.svelte'

  onMount(() => {
    loadPrograms()
    loadZones()
  })

  async function toggle(p: Program) {
    try {
      await api('PUT', `/api/programs/${p.id}`, { ...p, enabled: !p.enabled })
      await loadPrograms()
    } catch (e) {
      toast(errMsg(e), true)
    }
  }

  async function runNow(p: Program) {
    try {
      await api('POST', `/api/programs/${p.id}/run`, {})
      toast(`${p.name}: ${t('run_now')}`)
    } catch (e) {
      toast(errMsg(e), true)
    }
  }
</script>

<header class="top">
  <h1>{t('programs')}</h1>
  <button class="primary" onclick={() => go('/programs/new')}><Icon name="plus" />{t('new_program')}</button>
</header>

{#each cache.programs as p (p.id)}
  <div class="card">
    <div class="row spread" style="align-items:flex-start">
      <span
        class="zone-num"
        style="background:{p.enabled ? 'var(--accent-soft)' : ''};color:{p.enabled ? 'var(--accent)' : ''}"
        ><Icon name="calendar" /></span
      >
      <a class="grow" href={'#/programs/' + p.id} style="color:inherit;text-decoration:none">
        <div style="font-weight:600;font-size:1.05rem" class:muted={!p.enabled}>{p.name}</div>
        <div class="small muted">{programSummary(p, cache.zones)}</div>
      </a>
      <input
        type="checkbox"
        checked={p.enabled}
        onchange={() => toggle(p)}
        aria-label={t('enabled')}
        title={p.enabled ? t('enabled') : t('disabled')}
      />
    </div>
    <div class="row" style="margin-top:12px">
      <button class="grow" onclick={() => runNow(p)}><Icon name="play" />{t('run_now')}</button>
      <a class="btn grow" href={'#/programs/' + p.id}><Icon name="edit" />{t('edit')}</a>
    </div>
  </div>
{:else}
  <p class="muted">—</p>
{/each}
