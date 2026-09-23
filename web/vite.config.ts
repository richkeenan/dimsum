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
    react({ compiler: true }),
    tailwindcss(),
  ],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  // Prerender fetches must use the same address family as the preview listener.
  preview: { host: "127.0.0.1" },
  server: {
    proxy: {
      "/api": process.env.DIMSUM_API_URL || "http://127.0.0.1:8080",
      "/session": process.env.DIMSUM_API_URL || "http://127.0.0.1:8080",
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test-setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
