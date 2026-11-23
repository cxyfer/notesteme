/// <reference types="vitest/config" />
import { fileURLToPath } from "node:url";

import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Hub UI（M5-S9／UI-6）：獨立於 Client 的 Vite 專案，建置產物由 notesteme-hub 以 go:embed 提供（web/hub/embed.go）。
// 開發時 /api、/auth、/mcp 代理到 Hub（notesteme-hub serve，預設 127.0.0.1:8090）。
const backend = process.env.NOTESTEME_HUB_DEV_BACKEND ?? "http://127.0.0.1:8090";

export default defineConfig({
  plugins: [react()],
  resolve: {
    // web/shared（Client 與 Hub 共用的元件，ADR-0008 決策 9）；dedupe 讓 shared 的 react 解析到這裡的 node_modules。
    alias: { "@shared": fileURLToPath(new URL("../shared/src", import.meta.url)) },
    dedupe: ["react", "react-dom"],
  },
  build: {
    outDir: "dist",
    // dist/.gitkeep 要保留（go:embed 需要）：清理交給 scripts/clean-dist.mjs。
    emptyOutDir: false,
    // CSP 的 img-src／font-src 只允許 'self'：不內嵌成 data: URI。
    assetsInlineLimit: 0,
    sourcemap: false,
    target: "es2022",
  },
  server: {
    port: 5174,
    proxy: {
      "/api": { target: backend, changeOrigin: false },
      "/auth": { target: backend, changeOrigin: false },
      "/mcp": { target: backend, changeOrigin: false },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    maxWorkers: 1,
    testTimeout: 15_000,
  },
});
