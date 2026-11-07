import './app.css'
import { mount } from 'svelte'
import App from './App.svelte'
import { applyTheme, theme } from './lib/ui.svelte'

applyTheme(theme.value)
mount(App, { target: document.getElementById('app')! })

if ('serviceWorker' in navigator && location.protocol !== 'file:' && !import.meta.env.DEV) {
  navigator.serviceWorker.register('/sw.js').catch(() => {})
}
