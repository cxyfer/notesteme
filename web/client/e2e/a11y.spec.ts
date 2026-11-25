// NFR-A11Y-1、A11Y-2（目標 24×24px）與 NFR-CMP-2：每個頁面（src/test/a11yRoutes.ts）在三個引擎、三種寬度、
// 淺色與深色主題下跑完整的 axe（含 color-contrast、target-size），沒有 serious／critical。
// 同一次載入也截圖（design-language §9 的里程碑截圖對照；只在 Chromium、NOTESTEME_E2E_SHOTS=1 時）。
import { mkdirSync } from "node:fs";
import path from "node:path";

import { axeCheck } from "./support/axe";
import { e2eRoutes, slug, unmappedFixtureRoutes } from "./support/routes";
import { outDir } from "./support/server";
import { expect, settleTransitions, test, waitReady } from "./support/test";

export const WIDTHS = [
  { width: 375, height: 667 },
  { width: 768, height: 1024 },
  { width: 1280, height: 800 },
] as const;

const shots = process.env.NOTESTEME_E2E_SHOTS === "1";

/**
 * 引擎限制（docs/ui/README.md 10.32）：Playwright 的 Firefox 159 在 axe 4.14 的 `aria-conditional-attr` 規則
 * 分析新增抽屜的「調整這次的設定」區塊（`.add__run`）時，內容程序 SIGSEGV（signal 11，Page crashed），每次都重現；
 * 只跑這條規則、只分析這個區塊也會崩潰，個別元素與最小重現的 HTML 不會。不是頁面的 JS 例外。
 * 這一頁在 Firefox 標成 fixme；Chromium 與 WebKit 仍以完整規則檢查。不加排除規則。
 */
const FIREFOX_AXE_CRASH = new Map([
  [
    "/add?url=javascript%3Aalert(1)",
    "Firefox 159 在 axe 的 aria-conditional-attr 分析新增抽屜時內容程序 SIGSEGV（引擎問題，見 docs/ui/README.md 10.32）",
  ],
]);

test("a11yRoutes 每一條帶 fixture ID 的路由都對應到真資料", ({ env }) => {
  expect(unmappedFixtureRoutes(env)).toEqual([]);
});

for (const route of e2eRoutes()) {
  test.describe(`頁面 ${route.source}`, () => {
    if (!route.auth) test.use({ storageState: { cookies: [], origins: [] } });
    for (const vp of WIDTHS) {
      test(`axe ${vp.width}px 淺色與深色`, async ({ page, env }, info) => {
        test.fixme(
          info.project.name === "firefox" && FIREFOX_AXE_CRASH.has(route.source),
          FIREFOX_AXE_CRASH.get(route.source),
        );
        const r = route.at(env);
        await page.setViewportSize(vp);
        await page.emulateMedia({ colorScheme: "light" });
        await page.goto(r.path);
        await waitReady(page, r.heading, r.ready, r.dialog);
        const reports: string[] = [];
        // NFR-RWD-1：任何寬度都沒有整頁的水平捲軸（寬表格在自己的捲動區裡捲）
        const overflow = await page.evaluate(() => {
          const extra = document.documentElement.scrollWidth - window.innerWidth;
          if (extra <= 0) return null;
          // 超出視窗的元素裡，最外層的那些（沒有同樣超出的父元素）
          const all = [...document.querySelectorAll<HTMLElement>("body *")].filter(
            (e) => e.getBoundingClientRect().right > window.innerWidth + 1,
          );
          const wide = all
            .filter((e) => !all.includes(e.parentElement!))
            .slice(0, 5)
            .map((e) => `${e.tagName.toLowerCase()}.${e.className}`);
          return `多出 ${extra}px：${wide.join("、")}`;
        });
        if (overflow)
          reports.push(
            `${route.source}（${info.project.name}、${vp.width}px）頁面有水平捲軸，${overflow}`,
          );
        for (const theme of ["light", "dark"] as const) {
          await page.emulateMedia({ colorScheme: theme });
          // 主題切換的顏色轉場要結束，axe 才量到最終的顏色
          await settleTransitions(page);
          const report = await axeCheck(page, info, {
            route: route.source,
            width: vp.width,
            theme,
          });
          if (report) reports.push(report);
          if (shots && info.project.name === "chromium") {
            const dir = path.join(outDir, "screenshots", `${vp.width}`, theme);
            mkdirSync(dir, { recursive: true });
            await page.screenshot({
              path: path.join(dir, `${slug(route.source)}.jpg`),
              fullPage: true,
              type: "jpeg",
              quality: 70,
              animations: "disabled",
            });
          }
        }
        expect(reports, reports.join("\n\n")).toEqual([]);
      });
    }
  });
}
