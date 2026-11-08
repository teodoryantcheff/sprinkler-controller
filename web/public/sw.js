// Minimal service worker: makes the UI installable and lets the shell load
// offline. API calls always go to the network.
const CACHE = 'sprinkler-v2'
self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(['/', '/icon.svg', '/icon-192.png', '/manifest.webmanifest'])))
  self.skipWaiting()
})
self.addEventListener('activate', (e) => {
  e.waitUntil(caches.keys().then((ks) => Promise.all(ks.filter((k) => k !== CACHE).map((k) => caches.delete(k)))))
  self.clients.claim()
})
self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url)
  if (e.request.method !== 'GET' || url.pathname.startsWith('/api/')) return
  e.respondWith(
    fetch(e.request)
      .then((r) => {
        if (r.ok && url.origin === location.origin) {
          const copy = r.clone()
          caches.open(CACHE).then((c) => c.put(e.request, copy))
        }
        return r
      })
      .catch(() => caches.match(e.request).then((r) => r || caches.match('/'))),
  )
})
