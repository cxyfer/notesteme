/// <reference types="vitest/config" />
import { fileURLToPath } from "node:url";

import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// 開發時由 Vite 提供前端，/api 與 /mcp 代理到 Go 後端（notesteme serve，預設 127.0.0.1:8080）。
const backend = process.env.NOTESTEME_DEV_BACKEND ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [react()],
  resolve: {
    // web/shared（Client 與 Hub 共用的元件，ADR-0008 決策 9）。它底下沒有 node_modules：
    // dedupe 讓 shared 裡的 react 解析到這裡的 node_modules，只有一份 React。
    alias: { "@shared": fileURLToPath(new URL("../shared/src", import.meta.url)) },
    dedupe: ["react", "react-dom"],
  },
  build: {
    outDir: "dist",
    // dist/.gitkeep 要保留（go:embed 需要）：清理交給 scripts/clean-dist.mjs。
    emptyOutDir: false,
    // 不把資源內嵌成 data: URI：CSP 的 img-src／font-src 只允許 'self'。
    assetsInlineLimit: 0,
    sourcemap: false,
    target: "es2022",
  },
  server: {
    proxy: {
      "/api": { target: backend, changeOrigin: false },
      "/mcp": { target: backend, changeOrigin: false },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    // 機器記憶體有限：單一 worker 依序執行。
    maxWorkers: 1,
    // 多個 child 同時跑時機器負載很高：預設 5 秒對整頁的互動測試太緊
    testTimeout: 15_000,
  },
});
