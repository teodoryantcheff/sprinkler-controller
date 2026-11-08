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
  import Icon from './lib/Icon.svelte'

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
    { path: '/', key: 'home', icon: 'home' },
    { path: '/programs', key: 'programs', icon: 'calendar' },
    { path: '/zones', key: 'zones', icon: 'droplet' },
    { path: '/history', key: 'history', icon: 'history' },
    { path: '/settings', key: 'settings', icon: 'settings' },
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
      <label class="field"
        ><span>{t('password')}</span>
        <input
          type="password"
          bind:value={password}
          autocomplete={session.setupRequired ? 'new-password' : 'current-password'}
          minlength={session.setupRequired ? 7 : undefined}
          required
        />
      </label>
      {#if error}<div class="chip bad">{error}</div>{/if}
      <button class="primary big" disabled={busy}>{session.setupRequired ? t('create') : t('login')}</button>
      <div class="row">
        <button type="button" class="ghost small" onclick={() => setLang(i18n.lang === 'en' ? 'bg' : 'en')}
          >{i18n.lang === 'en' ? 'Български' : 'English'}</button
        >
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
      <p>{t('not_found')} <a href="#/">{t('home')}</a></p>
    {/if}
  </main>
  <nav class="tabs" aria-label={t('main_nav')}>
    {#each tabs as tab}
      <a href={'#' + tab.path} aria-current={section === tab.path ? 'page' : undefined}>
        <span class="pill"><Icon name={tab.icon} size={28} /></span>
        <span>{t(tab.key)}</span>
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
