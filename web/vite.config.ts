import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";
import { tanstackStart } from "@tanstack/react-start/plugin/vite";
export default defineConfig({
  plugins: [
    !process.env.VITEST &&
      tanstackStart({
        spa: { enabled: true, prerender: { outputPath: "/index.html" } },
      }),
    react(),
    tailwindcss(),
  ],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  server: {
    proxy: {
      "/api": process.env.DIMSUM_API_URL || "http://127.0.0.1:18080",
      "/session": process.env.DIMSUM_API_URL || "http://127.0.0.1:18080",
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test-setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
