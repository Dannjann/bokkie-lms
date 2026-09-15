import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// The build output is embedded into the Go binary, so it must stay
// self-contained and relative: nothing here may assume a web server root other
// than the one bokkie serves it from.
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    strictPort: true,
    // In dev the Go server proxies to Vite, not the other way round, so this
    // dev server never needs to know about /api.
  },
})
