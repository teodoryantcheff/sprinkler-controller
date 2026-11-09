# Phone setup

1. **HTTPS (once per phone).** The controller has its own small certificate authority, because no public one issues certificates for a home network. Open http://192.168.1.20/ca.crt (or Settings → App on this phone → Download certificate) and install it:
   - **Android:** Settings → Security → Encryption & credentials → Install a certificate → CA certificate.
   - **iPhone:** Settings → Profile Downloaded → Install, then Settings → General → About → Certificate Trust Settings → turn it on.

   After that, https://192.168.1.20 opens without warnings. Plain http:// keeps working too.
2. **Add to the home screen.**
   - **Android (Chrome):** open the **https** address. An **Install app** button appears on Home and in Settings; the ⋮ menu → Install app also works. Over plain http, Chrome only makes a browser shortcut.
   - **iPhone (Safari):** Share → Add to Home Screen. This works over http or https and opens full-screen like an app.

The certificate authority lives in `/var/lib/sprinkler/tls/` on the Pi (10 years). The server certificate (800 days, the most iOS accepts) is renewed on its own a month before it expires, and re-issued at start-up when the Pi gains a new IP address or name. Names are only added, never dropped, so a boot before the network is up (no address yet) keeps the certificate as it was. To drop old names, delete `server.crt` there and restart the service. Phones keep trusting it, because the authority stays the same.
