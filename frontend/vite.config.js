import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发期 Vite :5173，/api 与 /media 代理到 Go 后端 :8080（DESIGN §12）
export default defineConfig({
  // 部署基路径：默认 '/'（单端口/根路径部署）；
  // Nginx 前缀分流（如 /anidock）时构建传 ANIDOCK_BASE=/anidock/（Dockerfile 的 BASE_PATH 参数）
  base: process.env.ANIDOCK_BASE || '/',
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/media': 'http://localhost:8080',
    },
  },
})
