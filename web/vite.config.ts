import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'
import { resolve } from 'node:path'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: { alias: { '@': resolve(__dirname, 'src') } },
  build: { outDir: '../internal/web/dist', emptyOutDir: true },
  server: { proxy: { '/api': { target: 'https://127.0.0.1:8443', secure: false } } },
})
