// Small shared UI state: toasts, router, theme.

export const toastState = $state<{ msg: string; error: boolean; id: number }>({ msg: '', error: false, id: 0 })

export function toast(msg: string, error = false) {
  const id = ++toastState.id
  toastState.msg = msg
  toastState.error = error
  setTimeout(() => { if (toastState.id === id) toastState.msg = '' }, error ? 5000 : 2500)
}

export function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

export const route = $state({ path: location.hash.slice(1) || '/' })
window.addEventListener('hashchange', () => { route.path = location.hash.slice(1) || '/' })

export function go(path: string) { location.hash = path }

export type Theme = 'auto' | 'light' | 'dark'
export const theme = $state<{ value: Theme }>({ value: (localStorage.getItem('theme') as Theme) || 'auto' })

export function applyTheme(v: Theme) {
  theme.value = v
  localStorage.setItem('theme', v)
  if (v === 'auto') delete document.documentElement.dataset.theme
  else document.documentElement.dataset.theme = v
}
