import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: { proxy: { "/api": "http://localhost:8080" } },
  // three.js (the Universe in 3D) is its own file of about 600 kB, loaded only when 3D is chosen.
  build: { outDir: "dist", emptyOutDir: true, chunkSizeWarningLimit: 700 },
});
