// e2e 共用的 test：
//   - NFR-TEST-1：瀏覽器的每個請求只放行 127.0.0.1，其餘（YouTube 嵌入播放器、i.ytimg 縮圖等）一律 abort 並記下來；
//     被擋下的請求附在測試報告裡（不是失敗：頁面本來就會嘗試載入嵌入的播放器）。
//   - env：global setup 寫下的資料（影音 ID 等）。
import { readFileSync } from "node:fs";
import path from "node:path";

import { test as base, expect, type Page } from "@playwright/test";

import type { E2EEnv } from "./global-setup";
import { outDir } from "./server";

export function loadEnv(): E2EEnv {
  return JSON.parse(readFileSync(path.join(outDir, "state", "env.json"), "utf8")) as E2EEnv;
}

export const test = base.extend<{ env: E2EEnv; blocked: string[] }>({
  env: async ({}, use) => {
    await use(loadEnv());
  },
  blocked: async ({}, use) => {
    await use([]);
  },
  context: async ({ context, blocked }, use, info) => {
    await context.route(
      (url) => url.hostname !== "127.0.0.1",
      (route) => {
        blocked.push(route.request().url());
        return route.abort("blockedbyclient");
      },
    );
    await use(context);
    if (blocked.length > 0) {
      await info.attach("blocked-external-requests.txt", {
        body: [...new Set(blocked)].join("\n"),
        contentType: "text/plain",
      });
    }
  },
});

export { expect };

/**
 * 等頁面「就緒」：h1 出現、沒有載入中的 spinner。SSE（/api/events）一直連著，不能等 networkidle。
 * ready 給了的話再等那段文字出現。
 */
export async function waitReady(page: Page, heading: string | RegExp, ready?: string | RegExp) {
  await expect(page.getByRole("heading", { level: 1, name: heading }).first()).toBeVisible();
  if (ready) await expect(page.getByText(ready).first()).toBeVisible();
  await expect(page.locator(".spinner")).toHaveCount(0);
  // 字型與圖片載完，版面不再移動（axe 的對比與目標大小依實際排版）
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
}

/** 等有限次數的動畫與轉場結束（無限的「顯影」與 spinner 不算）。 */
export async function settleTransitions(page: Page) {
  await page.waitForFunction(() =>
    document
      .getAnimations()
      .every(
        (a) => a.playState !== "running" || a.effect?.getComputedTiming().iterations === Infinity,
      ),
  );
}
