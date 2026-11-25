// 產生 public/third-party-notices.txt：隨 Web UI 散布（內嵌進 binary、從 /third-party-notices.txt 提供）的字型與圖示，
// 依授權（SIL OFL 1.1、MIT）要附上的版權聲明與授權全文，原文取自各套件的 LICENSE。
// 加了新的字型或圖示套件時加進 PACKAGES，執行 `npm run gen:notices`；src/notices.test.ts 檢查檔案與套件一致。
import { readFileSync, writeFileSync } from "node:fs";

export const PACKAGES = [
  "@fontsource/barlow",
  "@fontsource/barlow-semi-condensed",
  "@fontsource/source-serif-4",
  "@fontsource/noto-serif-tc",
  "@fontsource/noto-serif-sc",
];

export function renderNotices(root = new URL("../", import.meta.url)) {
  const parts = [
    "Notesteme Web UI 隨附的第三方字型與圖示（third-party fonts and icons bundled with the Notesteme Web UI）",
    "",
  ];
  for (const name of PACKAGES) {
    const pkg = JSON.parse(
      readFileSync(new URL(`node_modules/${name}/package.json`, root), "utf8"),
    );
    const license = readFileSync(new URL(`node_modules/${name}/LICENSE`, root), "utf8").trim();
    parts.push(
      "=".repeat(72),
      `${name}@${pkg.version}（${pkg.license}）`,
      "=".repeat(72),
      "",
      license,
      "",
    );
  }
  return parts.join("\n");
}

if (import.meta.url === `file://${process.argv[1]}`) {
  writeFileSync(new URL("../public/third-party-notices.txt", import.meta.url), renderNotices());
}
