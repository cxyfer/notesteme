/// <reference types="vitest/config" />
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// 開發時由 Vite 提供前端，/api 與 /mcp 代理到 Go 後端（notesteme serve，預設 127.0.0.1:8080）。
const backend = process.env.NOTESTEME_DEV_BACKEND ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [react()],
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
  },
});
