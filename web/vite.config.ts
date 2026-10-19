import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Em desenvolvimento (npm run dev), /api vai para os serviços do docker-compose,
// do mesmo jeito que o nginx faz na imagem de produção:
//   GET  /api/orders/{id}/tracking -> tracking-service (8081)
//   POST /api/orders               -> dispatch-service (8080)
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5174,
    proxy: {
      '^/api/orders/[^/]+/tracking$': {
        target: 'http://localhost:8081',
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
      '/api': {
        target: 'http://localhost:8080',
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
    },
  },
})
