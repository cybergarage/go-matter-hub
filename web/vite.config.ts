import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// `npm run dev` serves the UI on :5173 and forwards the API to a hub started
// with its default address. The Host header is forwarded unchanged, so the
// hub sees the page and the API as one origin.
export default defineConfig({
  plugins: [svelte()],
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8580',
    },
  },
  build: {
    // public/.gitkeep is copied to dist/.gitkeep, which keeps the directory
    // in the repository for go:embed.
    outDir: 'dist',
    emptyOutDir: true,
  },
});
