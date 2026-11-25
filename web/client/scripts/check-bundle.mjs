// NFR-PERF-10：初始載入的 JS（gzip 後）< 300KB。
// 「初始載入」＝ dist/index.html 直接引用的 module script 加上 modulepreload，各自 gzip 後加總；
// 之後才 lazy import 的 chunk 不算。同時確認 index.html 沒有 inline script（CSP 的 script-src 'self'）。
import { readdirSync, readFileSync } from "node:fs";
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

// design-language.md §4.1：閱讀字型只在閱讀區用到時才載入。初始 CSS（index.html 直接引用的 stylesheet）
// 不能宣告 Source Serif 4／Noto Serif 的 @font-face；字型只用 woff2，也不能內嵌成 data:（CSP font-src 'self'）。
for (const m of html.matchAll(/<link\b[^>]*rel="stylesheet"[^>]*>/gi)) {
  const href = /\shref="([^"]+)"/i.exec(m[0]);
  if (!href) continue;
  const css = readFileSync(new URL(href[1].replace(/^\//, ""), dist), "utf8");
  for (const face of css.matchAll(/@font-face\s*\{[^}]*\}/g))
    if (/Source Serif|Noto Serif/.test(face[0])) {
      console.error(`${href[1]} 宣告了閱讀字型的 @font-face：它們只能跟著筆記的 lazy chunk 載入`);
      process.exit(1);
    }
}
const assets = readdirSync(new URL("assets/", dist));
const fonts = assets.filter((f) => /\.(woff2?|ttf|otf|eot)$/.test(f));
const notWoff2 = fonts.filter((f) => !f.endsWith(".woff2"));
if (notWoff2.length) {
  console.error(`字型只用 woff2：${notWoff2.join(", ")}`);
  process.exit(1);
}
for (const f of assets.filter((f) => f.endsWith(".css")))
  if (
    /url\(\s*["']?data:(font|application)/.test(readFileSync(new URL(`assets/${f}`, dist), "utf8"))
  ) {
    console.error(`${f} 把字型內嵌成 data: URI：CSP 的 font-src 只允許 'self'`);
    process.exit(1);
  }
console.log(`字型：${fonts.length} 個 woff2，初始 CSS 沒有閱讀字型的 @font-face`);
