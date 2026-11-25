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

test("a11yRoutes 每一條帶 fixture ID 的路由都對應到真資料", ({ env }) => {
  expect(unmappedFixtureRoutes(env)).toEqual([]);
});

for (const route of e2eRoutes()) {
  test.describe(`頁面 ${route.source}`, () => {
    if (!route.auth) test.use({ storageState: { cookies: [], origins: [] } });
    for (const vp of WIDTHS) {
      test(`axe ${vp.width}px 淺色與深色`, async ({ page, env }, info) => {
        const r = route.at(env);
        await page.setViewportSize(vp);
        await page.emulateMedia({ colorScheme: "light" });
        await page.goto(r.path);
        await waitReady(page, r.heading, r.ready);
        const reports: string[] = [];
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
