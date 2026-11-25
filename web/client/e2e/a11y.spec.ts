// NFR-A11Y-1、A11Y-2（目標 24×24px）與 NFR-CMP-2：每個頁面（src/test/a11yRoutes.ts）在三個引擎、三種寬度、
// 淺色與深色主題下跑完整的 axe（含 color-contrast、target-size），沒有 serious／critical。
// 同一次載入也截圖（design-language §9 的里程碑截圖對照；只在 Chromium、NOTESTEME_E2E_SHOTS=1 時）。
import { mkdirSync } from "node:fs";
import path from "node:path";

import type { Page } from "@playwright/test";

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
 * 引擎限制（docs/ui/README.md 10.32 第 12 點）：Playwright 的 Firefox 159 在 axe 4.14 執行中，內容程序會 SIGSEGV
 * （Page crashed）。和規則、注入方式、等待方式都無關：`/add` 每次都崩潰，其他頁面零星（`/subscriptions/{id}` 約 1～2 成）。
 * 只針對這個錯誤，在新分頁做同樣的載入與檢查，最多 3 次，每次記在 annotation；3 次都崩潰才以具名的 fixme 標出
 * （同一頁仍由 Chromium 與 WebKit 以完整規則檢查）。其他錯誤照常失敗。不加排除規則。
 */
const FIREFOX_CRASH_ATTEMPTS = 3;

test("a11yRoutes 每一條帶 fixture ID 的路由都對應到真資料", ({ env }) => {
  expect(unmappedFixtureRoutes(env)).toEqual([]);
});

for (const route of e2eRoutes()) {
  test.describe(`頁面 ${route.source}`, () => {
    if (!route.auth) test.use({ storageState: { cookies: [], origins: [] } });
    for (const vp of WIDTHS) {
      test(`axe ${vp.width}px 淺色與深色`, async ({ page, env }, info) => {
        const r = route.at(env);
        const load = async (p: Page, theme: "light" | "dark") => {
          await p.setViewportSize(vp);
          await p.emulateMedia({ colorScheme: theme });
          await p.goto(r.path);
          await waitReady(p, r.heading, r.ready, r.dialog);
        };
        await load(page, "light");
        let current = page;
        const reports: string[] = [];
        // NFR-RWD-1：任何寬度都沒有整頁的水平捲軸（寬表格在自己的捲動區裡捲）
        const overflow = await current.evaluate(() => {
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
          const ctx = { route: route.source, width: vp.width, theme };
          await current.emulateMedia({ colorScheme: theme });
          // 主題切換的顏色轉場要結束，axe 才量到最終的顏色
          await settleTransitions(current);
          let report: string | undefined;
          for (let attempt = 1; report === undefined; attempt++) {
            try {
              report = await axeCheck(current, info, ctx);
            } catch (e) {
              const crashed = info.project.name === "firefox" && /Page crashed/.test(String(e));
              if (!crashed) throw e;
              info.annotations.push({
                type: "firefox-content-crash",
                description: `${route.source} ${vp.width}px ${theme}：第 ${attempt} 次 axe 執行中 Page crashed`,
              });
              test.fixme(
                attempt >= FIREFOX_CRASH_ATTEMPTS,
                `Firefox 的內容程序在 axe 執行中連續 ${attempt} 次 SIGSEGV（引擎問題，docs/ui/README.md 10.32 第 12 點）；這一頁由 Chromium 與 WebKit 檢查`,
              );
              current = await page.context().newPage();
              await load(current, theme);
              await settleTransitions(current);
            }
          }
          if (report) reports.push(report);
          if (shots && info.project.name === "chromium") {
            const dir = path.join(outDir, "screenshots", `${vp.width}`, theme);
            mkdirSync(dir, { recursive: true });
            await current.screenshot({
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
