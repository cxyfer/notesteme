// NFR-RWD-1：767px 以下（375×667）完成三件主要的事：查看進度、處理「需要你處理」、貼網址新增。
// 「新增」用假的 yt-dlp 辨識（e2e/support/server.ts，不連網路）。每一步都確認沒有整頁的水平捲軸、
// 按鈕至少 24×24px（WCAG 2.2 的 2.5.8）。真實手機的觸控、鍵盤遮擋與 PWA 仍是人工（docs/manual-acceptance.md 第 8 節）。
import type { Locator, Page } from "@playwright/test";

import { expect, test, waitReady } from "./support/test";

test.use({ viewport: { width: 375, height: 667 } });

async function noHorizontalScroll(page: Page) {
  const extra = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(extra, `${page.url()} 在 375px 有水平捲軸（多 ${extra}px）`).toBeLessThanOrEqual(0);
}

async function bigEnough(target: Locator) {
  const box = await target.boundingBox();
  expect(box, "目標沒有排版").not.toBeNull();
  expect(
    Math.min(box!.width, box!.height),
    `目標 ${box!.width}×${box!.height}px 小於 24px`,
  ).toBeGreaterThanOrEqual(24);
}

test.describe("NFR-RWD-1 375px", () => {
  test("導覽：底部分頁列，沒有側邊導覽", async ({ page }) => {
    await page.goto("/");
    await waitReady(page, "總覽");
    const tabs = page.getByRole("navigation", { name: "分頁列" });
    await expect(tabs).toBeVisible();
    await expect(page.locator("nav.nav")).toHaveCount(0);
    for (const name of ["總覽", "媒體庫", "新增", "佇列", "更多"]) {
      const item = tabs.getByRole(name === "新增" || name === "更多" ? "button" : "link", { name });
      await expect(item).toBeVisible();
      await bigEnough(item);
    }
  });

  test("查看進度：總覽與佇列", async ({ page }) => {
    await page.goto("/");
    await waitReady(page, "總覽");
    await expect(page.getByRole("heading", { name: "現在" })).toBeVisible();
    await noHorizontalScroll(page);
    await page
      .getByRole("navigation", { name: "分頁列" })
      .getByRole("link", { name: "佇列" })
      .click();
    await waitReady(page, "佇列", "下載");
    await noHorizontalScroll(page);
    // 媒體庫的列有文字版的狀態（不是只有時間帶）
    await page
      .getByRole("navigation", { name: "分頁列" })
      .getByRole("link", { name: "媒體庫" })
      .click();
    await waitReady(page, "媒體庫");
    await expect(page.getByRole("img", { name: /字幕|分段|完成/ }).first()).toBeVisible();
    await noHorizontalScroll(page);
  });

  test("處理「需要你處理」：從總覽重新處理失敗的步驟", async ({ page, env }) => {
    // 用最後一部：其他測試（a11y）只需要還有一部留在清單上
    const id = env.failedItems[env.failedItems.length - 1]!;
    await page.goto("/");
    await waitReady(page, "總覽", "需要你處理");
    const needs = page.locator("section", {
      has: page.getByRole("heading", { name: "需要你處理", exact: true }),
    });
    const row = needs.locator("li", { has: page.locator(`a[href$="/items/${id}"]`) });
    await expect(row).toBeVisible();
    await noHorizontalScroll(page);
    const view = row.getByRole("button", { name: "查看" });
    await bigEnough(view);
    await view.click();
    await waitReady(page, /^Video \d+ about/);
    const rerun = page.getByRole("button", { name: "從分段重新處理" });
    await expect(rerun).toBeVisible();
    await bigEnough(rerun);
    await noHorizontalScroll(page);
    await rerun.click();
    // 重新處理的對話框（有的話）：確認
    const dialog = page.getByRole("dialog");
    if (await dialog.isVisible().catch(() => false)) {
      const start = dialog.getByRole("button", { name: /開始|重新處理/ }).last();
      await bigEnough(start);
      await start.click();
    }
    // 結果：步驟不再是失敗（按鈕消失），回到總覽後這部不在「需要你處理」
    await expect(rerun).toBeHidden({ timeout: 30_000 });
    await page
      .getByRole("navigation", { name: "分頁列" })
      .getByRole("link", { name: "總覽" })
      .click();
    await waitReady(page, "總覽");
    await expect(row).toHaveCount(0, { timeout: 30_000 });
  });

  test("貼網址新增：底部「新增」→ 辨識 → 開始處理 → 媒體庫出現新項目", async ({ page }) => {
    await page.goto("/library");
    await waitReady(page, "媒體庫");
    await page
      .getByRole("navigation", { name: "分頁列" })
      .getByRole("button", { name: "新增" })
      .click();
    const drawer = page.getByRole("dialog", { name: "新增" });
    await expect(drawer).toBeVisible();
    await drawer.getByLabel("網址").fill("https://www.youtube.com/watch?v=e2eRwdNew01");
    // 辨識結果（網址的形狀；標題要等處理開始後由假的 yt-dlp 取得）
    await expect(drawer.getByRole("region", { name: "YouTube 影片" })).toContainText("e2eRwdNew01");
    await expect(drawer.getByText(/預估花費/)).toBeVisible();
    const submit = drawer.getByRole("button", { name: "開始處理" });
    await bigEnough(submit);
    await noHorizontalScroll(page);
    await submit.click();
    await expect(page.getByText(/已開始處理|已經在媒體庫中/).first()).toBeVisible();
    await expect(drawer).toBeHidden();
    await page.goto("/library?q=e2eRwdNew01");
    await waitReady(page, "媒體庫");
    await expect(page.getByText("E2E 新影片 e2eRwdNew01").first()).toBeVisible();
    await noHorizontalScroll(page);
  });
});
