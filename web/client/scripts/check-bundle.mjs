// NFR-PERF-10：初始載入的 JS（gzip 後）< 300KB。
// 「初始載入」＝ dist/index.html 直接引用的 module script 加上 modulepreload，各自 gzip 後加總；
// 之後才 lazy import 的 chunk 不算。同時確認 index.html 沒有 inline script（CSP 的 script-src 'self'）。
import { readFileSync } from "node:fs";
import { gzipSync } from "node:zlib";

const BUDGET = 300 * 1000; // bytes（300KB）
const dist = new URL("../dist/", import.meta.url);
const html = readFileSync(new URL("index.html", dist), "utf8");

const files = new Set();
for (const m of html.matchAll(/<script\b([^>]*)>/gi)) {
  const src = /\ssrc="([^"]+)"/i.exec(m[1]);
  if (!src) {
    console.error("dist/index.html 有 inline <script>：CSP 不允許");
    process.exit(1);
  }
  files.add(src[1]);
}
for (const m of html.matchAll(/<link\b[^>]*rel="modulepreload"[^>]*>/gi)) {
  const href = /\shref="([^"]+)"/i.exec(m[0]);
  if (href) files.add(href[1]);
}
if (files.size === 0) {
  console.error("dist/index.html 沒有引用任何 JS：建置是否成功？");
  process.exit(1);
}

let total = 0;
for (const f of files) {
  const buf = readFileSync(new URL(f.replace(/^\//, ""), dist));
  const gz = gzipSync(buf, { level: 9 }).length;
  total += gz;
  console.log(`${f}  ${(buf.length / 1000).toFixed(1)} KB → gzip ${(gz / 1000).toFixed(1)} KB`);
}
const pct = ((total / BUDGET) * 100).toFixed(0);
console.log(
  `初始載入 JS（gzip）：${(total / 1000).toFixed(1)} KB／預算 ${BUDGET / 1000} KB（${pct}%）`,
);
if (total >= BUDGET) {
  console.error("超出 NFR-PERF-10 的預算：把不在首屏的頁面改成 lazy route，或移除大型相依。");
  process.exit(1);
}
