import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The dashboard is served by the admin port of Tollgate. "npm run dev"
// sends the API calls to a Tollgate on the default admin address.
export default defineConfig({
  plugins: [react()],
  base: '/',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    assetsDir: 'assets',
    sourcemap: false,
  },
  server: {
    proxy: { '/admin': 'http://127.0.0.1:9090' },
  },
});
