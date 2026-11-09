import { writeFileSync } from 'node:fs'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// dist/ is embedded into the Go binary; keep a tracked placeholder so `go build` works before the first web build.
const keepDist = {
  name: 'keep-dist',
  closeBundle() {
    writeFileSync('dist/.keep', '')
  },
}

export default defineConfig({
  plugins: [vue(), keepDist],
  build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 1500 },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/admin/api': 'http://127.0.0.1:8080',
    },
  },
})
