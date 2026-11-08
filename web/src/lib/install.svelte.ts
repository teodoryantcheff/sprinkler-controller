// "Add to home screen" support. Chrome/Edge on Android fire
// beforeinstallprompt when the app is installable (HTTPS with a trusted
// certificate); iOS Safari has no prompt, only Share → Add to Home Screen.

type PromptEvent = Event & { prompt: () => Promise<void>; userChoice: Promise<{ outcome: string }> }

let deferred: PromptEvent | null = null

const ua = navigator.userAgent
export const install = $state({
  canPrompt: false,
  installed:
    window.matchMedia('(display-mode: standalone)').matches ||
    (navigator as unknown as { standalone?: boolean }).standalone === true,
  ios: /iPhone|iPad|iPod/.test(ua) || (ua.includes('Macintosh') && navigator.maxTouchPoints > 1),
  android: /Android/.test(ua),
  secure: window.isSecureContext,
})

window.addEventListener('beforeinstallprompt', (e) => {
  e.preventDefault()
  deferred = e as PromptEvent
  install.canPrompt = true
})
window.addEventListener('appinstalled', () => {
  install.installed = true
  install.canPrompt = false
  deferred = null
})

export async function promptInstall() {
  if (!deferred) return
  await deferred.prompt()
  await deferred.userChoice.catch(() => null)
  deferred = null
  install.canPrompt = false
}
