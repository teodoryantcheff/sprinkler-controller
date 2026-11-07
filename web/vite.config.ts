import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// The built UI is embedded into the Go binary from internal/api/dist.
export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: '../internal/api/dist',
    emptyOutDir: false,
    target: 'es2020',
    cssCodeSplit: false,
  },
  server: {
    proxy: { '/api': { target: 'http://localhost:8080', changeOrigin: false } },
  },
})
