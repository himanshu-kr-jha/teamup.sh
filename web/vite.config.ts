import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { VitePWA } from 'vite-plugin-pwa';
import path from 'path';
import fs from 'fs';
import type { Plugin } from 'vite';

/**
 * MediaPipe's runtime for blurring or replacing a call's background, served by
 * TeamUp itself under /mediapipe/ rather than fetched from a CDN at call time:
 * it works on a network that blocks CDNs, and nobody's camera depends on a third
 * party. It is about 9 MB a variant, so it is copied out of node_modules at build
 * time instead of being committed. The model next to it is in public/mediapipe/.
 */
const mediapipeVersion: string = JSON.parse(
  fs.readFileSync(path.resolve(__dirname, 'node_modules/@mediapipe/tasks-vision/package.json'), 'utf8')
).version;
/** Named by version, so the server can let browsers keep it for good. */
const MEDIAPIPE_WASM = `/mediapipe/tasks-vision-${mediapipeVersion}/wasm`;

function mediapipeRuntime(): Plugin {
  const from = path.resolve(__dirname, 'node_modules/@mediapipe/tasks-vision/wasm');
  let outDir = '';
  let building = false;
  return {
    name: 'teamup-mediapipe-runtime',
    configResolved(config) {
      outDir = path.resolve(config.root, config.build.outDir);
      // Only a real build; Vitest runs the plugins too, with a made-up outDir.
      building = config.command === 'build' && !process.env.VITEST;
    },
    configureServer(server) {
      server.middlewares.use(MEDIAPIPE_WASM, (req, res, next) => {
        const name = path.basename((req.url ?? '').split('?')[0]);
        const file = path.join(from, name);
        if (!name || !fs.existsSync(file)) return next();
        res.setHeader('Content-Type', name.endsWith('.wasm') ? 'application/wasm' : 'text/javascript');
        fs.createReadStream(file).pipe(res);
      });
    },
    closeBundle() {
      if (!building) return;
      const to = path.join(outDir, MEDIAPIPE_WASM.slice(1));
      fs.mkdirSync(to, { recursive: true });
      for (const name of fs.readdirSync(from)) fs.copyFileSync(path.join(from, name), path.join(to, name));
    },
  };
}

export default defineConfig({
  define: { __MEDIAPIPE_WASM__: JSON.stringify(MEDIAPIPE_WASM) },
  plugins: [
    react(),
    mediapipeRuntime(),
    VitePWA({
      // Not autoUpdate: that reloads every open tab when a new version activates, and a
      // reload drops whoever is in a call there. src/sw/register.ts registers the worker
      // and offers the reload itself, so the plugin must not inject its own registration.
      registerType: 'prompt',
      injectRegister: false,
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
        // Push handling, pulled into the worker Workbox generates. Building our
        // own worker instead would mean reimplementing everything below.
        importScripts: ['/push-sw.js'],
        runtimeCaching: [
          {
            urlPattern: /^https:\/\/.*\/api\/(channels|messages|tasks|users|presence)/,
            // Network first, the cache only when offline. Serving the cached
            // copy first handed every refetch the previous answer, so nothing
            // that invalidates a query could show new data over HTTPS.
            handler: 'NetworkFirst',
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
