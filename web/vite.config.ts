import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: '/dashboard/',
  server: {
    proxy: {
      '/admin': 'http://localhost:8081',
      '/proxy': 'http://localhost:8081',
      '/health': 'http://localhost:8081',
      '/v1': 'http://localhost:8081',
      '/dashboard/auth': 'http://localhost:8081',
      '/dashboard/extension.zip': 'http://localhost:8081',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
