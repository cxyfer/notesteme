// NFR-I18N-3：日期、數字、金額用 Intl 依介面語言格式化；時間碼不隨語言改變；顯示字幕的元素帶字幕自己的 lang。
// 解讀（docs/ui/README.md 10.32）：「不隨語言改變」只指時間碼；日期與金額要等於同一個瀏覽器在該語言的 Intl 輸出
// （三種語言本來就不同）。字形的實際觀感（简中內容用簡體字形）仍是人工（docs/manual-acceptance.md 第 7 節）。
import type { Page } from "@playwright/test";

import { expect, test } from "./support/test";

const LANGS = ["zh-Hant", "zh-Hans", "en"] as const;

async function useLang(page: Page, lang: string) {
  await page.addInitScript((l) => {
    try {
      localStorage.setItem("notesteme.lang", l);
    } catch {
      // 沒有 localStorage 時測試會在 <html lang> 的斷言失敗
    }
  }, lang);
}

/** 在頁面裡用該語言的 Intl 算出預期的字串（和瀏覽器的 ICU 一致） */
function intl(page: Page, lang: string, kind: "usd" | "date", value: number | string) {
  return page.evaluate(
    ([l, k, v]) =>
      k === "usd"
        ? new Intl.NumberFormat(l, {
            style: "currency",
            currency: "USD",
            maximumFractionDigits: 2,
          }).format(Number(v))
        : new Intl.DateTimeFormat(l, { dateStyle: "medium" }).format(new Date(String(v))),
    [lang, kind, value] as const,
  );
}

test.describe("NFR-I18N-3", () => {
  test("三種介面語言：<html lang>、金額與日期照 Intl，時間碼與字幕的 lang 不變", async ({
    page,
    env,
  }) => {
    const timecodes: Record<string, string[]> = {};
    for (const lang of LANGS) {
      await useLang(page, lang);
      // 影音詳情（原文英文）：標題與字幕句的 lang 是字幕自己的語言，不是介面語言
      await page.goto(`/items/${encodeURIComponent(env.enItemRef)}`);
      await expect(page.locator("html")).toHaveAttribute("lang", lang);
      const h1 = page.getByRole("heading", { level: 1 }).first();
      await expect(h1).toHaveText(/^Video 0 about/);
      await expect(h1).toHaveAttribute("lang", "en");
      const cues = page.locator(".cue__text");
      await expect(cues.first()).toBeVisible();
      const cueLangs = await cues.evaluateAll((els) => [
        ...new Set(els.map((e) => e.getAttribute("lang"))),
      ]);
      expect(cueLangs, `${lang}：字幕句的 lang`).toEqual(["en"]);
      timecodes[lang] = await page
        .locator(".cue__time")
        .evaluateAll((els) => els.slice(0, 8).map((e) => (e.textContent ?? "").trim()));
      for (const tc of timecodes[lang]!) expect(tc).toMatch(/^(\d+:)?\d{2}:\d{2}$/);

      // 繁中筆記的閱讀區帶筆記自己的 lang（介面語言是简中或英文時也一樣）
      await page.goto(`/items/${env.noteItem}?tab=note`);
      const heading = page.getByText("重點").first();
      await expect(heading).toBeVisible();
      expect(await heading.evaluate((e) => e.closest("[lang]")?.getAttribute("lang"))).toBe(
        "zh-Hant",
      );

      // 金額：成本頁的 $1,234.5678（e2eseed 寫入）照 Intl 的貨幣格式
      await page.goto("/costs");
      await expect(page.getByText(await intl(page, lang, "usd", 1234.5678)).first()).toBeVisible();

      // 日期：媒體庫第 0 部的發布日（benchdata：2025-01-01T00:00Z，瀏覽器時區 Asia/Taipei）
      await page.goto("/library?q=Video%200%20about");
      await expect(page.getByText(/^Video 0 about/).first()).toBeVisible();
      await expect(
        page.getByText(await intl(page, lang, "date", "2025-01-01T00:00:00Z")).first(),
      ).toBeVisible();
    }
    expect(timecodes["zh-Hans"], "简中的時間碼和繁中不同").toEqual(timecodes["zh-Hant"]);
    expect(timecodes.en, "英文的時間碼和繁中不同").toEqual(timecodes["zh-Hant"]);
    expect(timecodes["zh-Hant"]!.length).toBeGreaterThan(3);
  });
});
