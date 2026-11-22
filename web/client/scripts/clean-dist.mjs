// 建置前清空 dist，但保留 .gitkeep（go:embed all:dist 需要它，沒有建置時 Go 也能編譯）。
// vite.config.ts 設 emptyOutDir: false，所以清理只在這裡做。
import { mkdirSync, readdirSync, rmSync } from "node:fs";
import { join } from "node:path";

const dist = new URL("../dist/", import.meta.url).pathname;
mkdirSync(dist, { recursive: true });
for (const name of readdirSync(dist)) {
  if (name !== ".gitkeep") rmSync(join(dist, name), { recursive: true, force: true });
}
