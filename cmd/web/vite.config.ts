import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  // graph/ui 可能自带 node_modules/react。两份 React 时，内核里的 useState 读到空 dispatcher。
  resolve: {
    preserveSymlinks: true,
    dedupe: ['react', 'react-dom'],
  },
  server: {
    fs: { allow: ['..', '../..'] },
    proxy: { '/api': 'http://localhost:8080' },
  },
})
