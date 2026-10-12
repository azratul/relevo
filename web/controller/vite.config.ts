import { defineConfig } from "vite";

// The relay serves dist/ (embedded). In dev, proxy the API to a local relay.
export default defineConfig({
  build: { outDir: "dist", emptyOutDir: false, modulePreload: { polyfill: false }, sourcemap: false },
  server: { proxy: { "/api": { target: "http://127.0.0.1:8080", ws: true } } },
});
