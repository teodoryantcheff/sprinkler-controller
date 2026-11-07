<script lang="ts">
  import { onMount } from 'svelte'
  import { api, checkSession, session, startLive, stopLive, live, loadZones, loadPrograms } from './lib/api.svelte'
  import { t, setLang, i18n } from './lib/i18n.svelte'
  import { route, toastState, errMsg } from './lib/ui.svelte'
  import Home from './pages/Home.svelte'
  import Programs from './pages/Programs.svelte'
  import ProgramEdit from './pages/ProgramEdit.svelte'
  import Zones from './pages/Zones.svelte'
  import History from './pages/History.svelte'
  import Settings from './pages/Settings.svelte'

  let password = $state('')
  let error = $state('')
  let busy = $state(false)

  async function boot() {
    try {
      const me = await checkSession()
      if (!localStorage.getItem('lang') && (me.language === 'bg' || me.language === 'en')) setLang(me.language)
      if (session.loggedIn) {
        startLive()
        await Promise.all([loadZones(), loadPrograms()])
      }
    } catch (e) {
      error = errMsg(e)
    }
  }
  onMount(() => {
    document.documentElement.lang = i18n.lang
    boot()
    return stopLive
  })

  async function submit(e: Event) {
    e.preventDefault()
    busy = true
    error = ''
    try {
      await api('POST', session.setupRequired ? '/api/setup' : '/api/login', { name: 'admin', password })
      password = ''
      await boot()
    } catch (err) {
      error = errMsg(err)
    } finally {
      busy = false
    }
  }

  const tabs = [
    { path: '/', key: 'home', icon: 'M3 11l9-7 9 7v9a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z' },
    { path: '/programs', key: 'programs', icon: 'M7 3v3M17 3v3M4 8h16M5 5h14a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zM8 12h3v3H8z' },
    { path: '/zones', key: 'zones', icon: 'M12 3c-4 6-7 9-7 13a7 7 0 0 0 14 0c0-4-3-7-7-13z' },
    { path: '/history', key: 'history', icon: 'M12 7v5l3 2M3 12a9 9 0 1 0 3-6.7L3 8M3 3v5h5' },
    { path: '/settings', key: 'settings', icon: 'M4 6h10M18 6h2M4 12h4M12 12h8M4 18h12M20 18h0M14 4v4M8 10v4M16 16v4' },
  ] as const

  const section = $derived('/' + (route.path.split('/')[1] || ''))
</script>

{#if !session.checked}
  <div class="wrap muted">{error || t('loading')}</div>
{:else if !session.loggedIn}
  <div class="wrap">
    <form class="card stack" style="max-width:380px;margin:12vh auto 0" onsubmit={submit}>
      <h1>{session.deviceName || 'Sprinkler'}</h1>
      {#if session.setupRequired}
        <p class="muted small">{t('setup_title')}. {t('setup_hint')}</p>
      {/if}
      <label class="field"><span>{t('password')}</span>
        <input type="password" bind:value={password} autocomplete={session.setupRequired ? 'new-password' : 'current-password'} minlength={session.setupRequired ? 8 : undefined} required />
      </label>
      {#if error}<div class="chip bad">{error}</div>{/if}
      <button class="primary big" disabled={busy}>{session.setupRequired ? t('create') : t('login')}</button>
      <div class="row">
        <button type="button" class="ghost small" onclick={() => setLang(i18n.lang === 'en' ? 'bg' : 'en')}>{i18n.lang === 'en' ? 'Български' : 'English'}</button>
      </div>
    </form>
  </div>
{:else}
  <main class="wrap">
    {#if route.path === '/'}
      <Home />
    {:else if route.path === '/programs'}
      <Programs />
    {:else if route.path.startsWith('/programs/')}
      {#key route.path}<ProgramEdit id={route.path.split('/')[2]} />{/key}
    {:else if route.path === '/zones'}
      <Zones />
    {:else if route.path === '/history'}
      <History />
    {:else if route.path === '/settings'}
      <Settings />
    {:else}
      <p>Not found. <a href="#/">{t('home')}</a></p>
    {/if}
  </main>
  <nav class="tabs" aria-label="Main">
    {#each tabs as tab}
      <a href={'#' + tab.path} aria-current={section === tab.path ? 'page' : undefined}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d={tab.icon} /></svg>
        {t(tab.key)}
      </a>
    {/each}
  </nav>
  {#if !live.connected && live.status}
    <div class="toast" role="status">{t('offline')}</div>
  {/if}
{/if}

{#if toastState.msg}
  <div class="toast" class:error={toastState.error} role="status">{toastState.msg}</div>
{/if}
