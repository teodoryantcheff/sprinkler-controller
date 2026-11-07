// Fails the build if the UI grows past the 150 KB gzipped budget.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { gzipSync } from 'node:zlib'
const dir = new URL('../../internal/api/dist/', import.meta.url).pathname
let total = 0
const walk = (d) => {
  for (const f of readdirSync(d)) {
    const p = join(d, f)
    if (statSync(p).isDirectory()) walk(p)
    else total += gzipSync(readFileSync(p)).length
  }
}
walk(dir)
const kb = (total / 1024).toFixed(1)
if (total > 150 * 1024) { console.error(`UI is ${kb} KB gzipped, over the 150 KB budget`); process.exit(1) }
console.log(`UI size: ${kb} KB gzipped (budget 150 KB)`)
