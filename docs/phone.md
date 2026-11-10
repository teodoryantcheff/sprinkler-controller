# Phone setup

Open http://192.168.1.20 (or http://sprinklerpi.local) and log in. Nothing needs installing on the phone.

**Add to the home screen.**

- **iPhone (Safari):** Share → Add to Home Screen. It opens full-screen like an app.
- **Android (Chrome):** ⋮ menu → Add to Home screen. Over plain http this is a browser shortcut; the full **Install app** (Settings → App on this phone) needs HTTPS with a trusted certificate.

The controller has no HTTPS of its own: the built-in local certificate authority was removed on 2026-11-10, because every phone that trusted it would also trust any certificate its key signed, for any site. HTTPS needs a certificate from elsewhere for a name the phone opens, set in `[tls]` (`listen`, `cert`, `key`); no public authority signs a LAN IP or a `.local` name.
