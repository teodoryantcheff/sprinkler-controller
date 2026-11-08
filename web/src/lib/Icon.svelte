<script lang="ts" module>
  // Inline 24×24 stroke icons. Kept in-repo (no icon font, no CDN) so the
  // bundle stays small and works offline on the Pi.
  const P = {
    home: 'M3 11l9-7 9 7v9a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z',
    calendar: 'M7 3v3M17 3v3M4 9h16M5 5h14a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zM8 13h3v3H8z',
    droplet: 'M12 3c-4 6-7 9-7 13a7 7 0 0 0 14 0c0-4-3-7-7-13z',
    history: 'M12 7v5l3 2M3 12a9 9 0 1 0 3-6.7L3 8M3 3v5h5',
    settings: 'M4 6h9M17 6h3M4 12h3M11 12h9M4 18h11M19 18h1M15 4v4M9 10v4M17 16v4',
    play: 'M7 4.5v15l12-7.5z',
    stop: 'M6.5 6.5h11v11h-11z',
    pause: 'M9 5v14M15 5v14',
    x: 'M6 6l12 12M18 6L6 18',
    plus: 'M12 5v14M5 12h14',
    minus: 'M5 12h14',
    back: 'M19 12H5M11 6l-6 6 6 6',
    up: 'M12 19V5M6 11l6-6 6 6',
    grip: 'M9 6h.01M15 6h.01M9 12h.01M15 12h.01M9 18h.01M15 18h.01',
    edit: 'M4 20h4L19 9l-4-4L4 16zM14 6l4 4',
    trash: 'M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3',
    sun: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4',
    rain: 'M7 15a4 4 0 0 1-.5-8A6 6 0 0 1 18 8.5 3.5 3.5 0 0 1 17.5 15zM8 18l-1 3M12 18l-1 3M16 18l-1 3',
    sensor: 'M12 21v-6M8 21h8M12 15a5 5 0 0 0 5-5c0-3-5-7-5-7s-5 4-5 7a5 5 0 0 0 5 5z',
    clock: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM12 7v5l3 2',
    list: 'M9 6h11M9 12h11M9 18h11M4 6h.01M4 12h.01M4 18h.01',
    activity: 'M3 12h4l3-8 4 16 3-8h4',
    logout: 'M15 4h4a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1h-4M10 17l-5-5 5-5M5 12h11',
    locate: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM12 2v3M12 19v3M2 12h3M19 12h3',
    download: 'M12 4v11M7 10l5 5 5-5M5 20h14',
    upload: 'M12 15V4M7 9l5-5 5 5M5 20h14',
    check: 'M5 12l5 5 9-10',
    alert: 'M12 3l10 18H2zM12 10v4M12 17h.01',
    key: 'M8 11a4 4 0 1 1 0 8 4 4 0 0 1 0-8zM11 13l9-9M16 8l3 3M14 10l2 2',
    chip: 'M7 7h10v10H7zM10 3v4M14 3v4M10 17v4M14 17v4M3 10h4M3 14h4M17 10h4M17 14h4',
    shield: 'M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z',
    globe: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM3 12h18M12 3c3 3 3 15 0 18M12 3c-3 3-3 15 0 18',
    pin: 'M12 21s-7-6.5-7-12a7 7 0 0 1 14 0c0 5.5-7 12-7 12zM12 7a2 2 0 1 0 0 4 2 2 0 0 0 0-4z',
    wifi: 'M2 9a15 15 0 0 1 20 0M5 12.5a10 10 0 0 1 14 0M8.5 16a5 5 0 0 1 7 0M12 19.5h.01',
    wifioff:
      'M3 3l18 18M8.5 16a5 5 0 0 1 7 0M12 19.5h.01M5 12.5a10 10 0 0 1 5-2.7M2 9a15 15 0 0 1 4.5-2.9M14.5 9.6a10 10 0 0 1 4.5 2.9M10.5 5.1A15 15 0 0 1 22 9',
  } as const
  export type IconName = keyof typeof P
  const FILLED: Partial<Record<IconName, true>> = { play: true, stop: true }
</script>

<script lang="ts">
  let { name, size = 24, label }: { name: IconName; size?: number | string; label?: string } = $props()
  const filled = $derived(!!FILLED[name])
</script>

<svg
  class="icon"
  viewBox="0 0 24 24"
  width={size}
  height={size}
  fill={filled ? 'currentColor' : 'none'}
  stroke="currentColor"
  stroke-width={name === 'grip' ? 3.2 : 2}
  stroke-linecap="round"
  stroke-linejoin="round"
  role={label ? 'img' : undefined}
  aria-label={label}
  aria-hidden={label ? undefined : 'true'}><path d={P[name]} /></svg
>
