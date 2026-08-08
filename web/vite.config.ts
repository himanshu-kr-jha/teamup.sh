import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { VitePWA } from 'vite-plugin-pwa';
import path from 'path';

export default defineConfig({
  plugins: [
    react(),
    VitePWA({
      registerType: 'autoUpdate',
      includeAssets: ['favicon.ico', 'robots.txt'],
      manifest: {
        name: 'TeamUp',
        short_name: 'TeamUp',
        description: 'Self-hosted collaboration platform',
        theme_color: '#4f46e5',
        background_color: '#ffffff',
        display: 'standalone',
        orientation: 'portrait-primary',
        scope: '/',
        start_url: '/',
        icons: [
          { src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
          { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
          { src: '/icons/icon-maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
        ],
        shortcuts: [
          { name: 'Messages', url: '/chat', description: 'Open chat' },
          { name: 'Tasks', url: '/tasks', description: 'Open tasks' },
          { name: 'Calls', url: '/calls', description: 'Start a call' },
        ],
      },
      workbox: {
        globPatterns: ['**/*.{js,css,html,ico,png,svg,woff2}'],
        runtimeCaching: [
          {
            urlPattern: /^https:\/\/.*\/api\/(channels|messages|tasks|users|presence)/,
            handler: 'StaleWhileRevalidate',
            options: {
              cacheName: 'api-get',
              expiration: { maxEntries: 200, maxAgeSeconds: 5 * 60 },
            },
          },
          {
            urlPattern: /^https:\/\/.*\/api\/.*/,
            handler: 'NetworkOnly',
            options: { cacheName: 'api-mutations' },
          },
          {
            urlPattern: /^https:\/\/.*\/livekit\/.*/,
            handler: 'NetworkOnly',
          },
        ],
      },
      devOptions: { enabled: true },
    }),
  ],
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') },
  },
  build: {
    outDir: '../cmd/teamup/web/dist',
    emptyOutDir: true,
    sourcemap: true,
    rollupOptions: {
      output: {
        manualChunks: {
          vendor: ['react', 'react-dom', 'react-router-dom'],
          query: ['@tanstack/react-query'],
          livekit: ['livekit-client', '@livekit/components-react'],
          ui: ['@radix-ui/react-dialog', '@radix-ui/react-dropdown-menu', '@radix-ui/react-tooltip'],
        },
      },
    },
  },
  server: {
    port: 3000,
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
      '/ws': { target: 'ws://localhost:8080', ws: true },
      '/auth': { target: 'http://localhost:8080', changeOrigin: true },
      '/wizard': { target: 'http://localhost:8080', changeOrigin: true },
      '/healthz': { target: 'http://localhost:8080', changeOrigin: true },
      '/metrics': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
});
