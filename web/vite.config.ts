import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// The Vue SPA is built into the Go package so it can be embedded via go:embed and
// served by the Go UI server. There is no Node runtime in the distroless image.
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    outDir: fileURLToPath(new URL('../internal/http/ui/dist', import.meta.url)),
    emptyOutDir: true,
  },
  server: {
    // During `npm run dev`, proxy API calls to the Go UI server.
    proxy: {
      '/api': {
        target: 'http://localhost:9092',
        changeOrigin: true,
      },
    },
  },
})
