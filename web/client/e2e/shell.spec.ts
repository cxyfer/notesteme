// G1（docs/ui/README.md 10.31）留下、只能在真實瀏覽器確認的三件事：
//   1. 768–1279px 窄側欄的 tooltip 不被主內容或其他元素蓋住（elementFromPoint）；
//   2. 很矮的視窗（1000×400）窄側欄不溢出版面，而且可以捲到最後一項；
//   3. 繁中筆記實際下載的字型切片：只有 Noto Serif TC 與拉丁字，不下載 SC 與日韓，總傳輸量有上限。
import type { Page } from "@playwright/test";

import { expect, test, waitReady } from "./support/test";

/** 窄側欄每個項目 hover 時，tooltip 的中心點是 tooltip 自己（沒有被蓋住、沒有被裁掉）。 */
async function tooltipsVisible(page: Page) {
  const items = page.locator(".nav .nav__item:has(> .nav__label), .nav .nav__add");
  const n = await items.count();
  expect(n, "窄側欄沒有任何項目").toBeGreaterThan(5);
  const problems: string[] = [];
  for (let i = 0; i < n; i++) {
    const item = items.nth(i);
    await item.scrollIntoViewIfNeeded();
    await item.hover();
    const r = await item.evaluate((el) => {
      const label = el.querySelector<HTMLElement>(".nav__label")!;
      // tooltip 是 pointer-events: none（不擋滑鼠），elementFromPoint 會穿過它：檢查時暫時打開
      label.style.pointerEvents = "auto";
      const b = label.getBoundingClientRect();
      const points = [
        [b.left + b.width / 2, b.top + b.height / 2],
        [b.left + 2, b.top + 2],
        [b.right - 2, b.bottom - 2],
      ] as const;
      const hits = points.map(([x, y]) => {
        const hit = document.elementFromPoint(x, y);
        return hit === label || label.contains(hit)
          ? null
          : `(${Math.round(x)},${Math.round(y)}) 是 ${hit?.tagName.toLowerCase()}.${hit?.className}`;
      });
      label.style.pointerEvents = "";
      return {
        text: label.textContent,
        width: b.width,
        inViewport: b.right <= window.innerWidth && b.left >= 0 && b.top >= 0,
        hits: hits.filter(Boolean),
      };
    });
    if (r.width < 8) problems.push(`「${r.text}」的 tooltip 沒有展開（寬 ${r.width}px）`);
    else if (!r.inViewport) problems.push(`「${r.text}」的 tooltip 超出視窗`);
    else if (r.hits.length > 0)
      problems.push(`「${r.text}」的 tooltip 被蓋住：${r.hits.join("；")}`);
  }
  return problems;
}

test.describe("G1 窄側欄", () => {
  for (const vp of [
    { width: 1000, height: 800 },
    { width: 768, height: 1024 },
  ]) {
    test(`tooltip 不被蓋住 ${vp.width}×${vp.height}（媒體庫與影音詳情）`, async ({ page, env }) => {
      await page.setViewportSize(vp);
      await page.goto("/library");
      await waitReady(page, "媒體庫");
      const lib = await tooltipsVisible(page);
      await page.goto(`/items/${env.noteItem}?tab=note`);
      await waitReady(page, /^Video 1 about/, "重點");
      const item = await tooltipsVisible(page);
      expect([...lib, ...item], [...lib, ...item].join("\n")).toEqual([]);
    });
  }

  for (const width of [1000, 1280]) {
    test(`很矮的視窗 ${width}×400：側欄不溢出、可以捲到最後一項`, async ({ page }) => {
      await page.setViewportSize({ width, height: 400 });
      await page.goto("/library");
      await waitReady(page, "媒體庫");
      const nav = page.locator("nav.nav");
      const m = await nav.evaluate((el) => {
        const s = getComputedStyle(el);
        const b = el.getBoundingClientRect();
        return {
          height: b.height,
          bottom: b.bottom,
          scrollHeight: el.scrollHeight,
          clientHeight: el.clientHeight,
          overflowY: s.overflowY,
          pageScrollX: document.documentElement.scrollWidth - window.innerWidth,
        };
      });
      // 側欄本身不高於視窗（sticky + 100vh），超出的內容在側欄內捲動，而不是被裁掉或撐開整頁
      expect(m.bottom, "窄側欄超出視窗底部").toBeLessThanOrEqual(400 + 0.5);
      expect(m.pageScrollX, "頁面出現水平捲軸").toBeLessThanOrEqual(0);
      if (m.scrollHeight > m.clientHeight) {
        expect(
          ["auto", "scroll"],
          `窄側欄的內容比視窗高（${m.scrollHeight}px），但不能捲動`,
        ).toContain(m.overflowY);
      }
      // 最後一項（窄側欄是「更多」、寬側欄是「登出」）捲進視窗後可以點到
      const last = nav
        .locator(width < 1280 ? ".nav__more .nav__item" : ".nav__footer button")
        .last();
      await last.scrollIntoViewIfNeeded();
      await expect(last).toBeInViewport({ ratio: 1 });
      if (width < 1280) {
        await last.click();
        await expect(page.getByRole("dialog")).toBeVisible();
        await page.keyboard.press("Escape");
        // 捲動之後 hover 最下面的項目，tooltip 仍在項目旁邊、沒有被蓋住
        expect(await tooltipsVisible(page)).toEqual([]);
      }
    });
  }
});

test.describe("G1 閱讀字型的切片", () => {
  // 不帶快取的新 context：計算這一頁真的下載了什麼
  test("繁中筆記只下載 Noto Serif TC 與拉丁字", async ({ page, env }, info) => {
    const fonts: { url: string; bytes: number }[] = [];
    page.on("response", (res) => {
      const url = res.url();
      if (!/\.woff2(\?|$)/.test(url)) return;
      void res
        .body()
        .then((b) => fonts.push({ url, bytes: b.length }))
        .catch(() => fonts.push({ url, bytes: 0 }));
    });
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto(`/items/${env.noteItem}?tab=note`);
    await waitReady(page, /^Video 1 about/, "重點");
    // 閱讀區的字串（繁中與拉丁）都畫出來、字型下載完
    await page.evaluate(() => document.fonts.ready.then(() => undefined));
    await page.waitForTimeout(500);
    const names = fonts.map((f) => decodeURIComponent(new URL(f.url).pathname.split("/").pop()!));
    const total = fonts.reduce((n, f) => n + f.bytes, 0);
    await info.attach("woff2.txt", {
      body: fonts
        .map((f) => `${f.bytes}\t${decodeURIComponent(new URL(f.url).pathname)}`)
        .concat(`total\t${total}`)
        .join("\n"),
      contentType: "text/plain",
    });
    expect(
      names.some((n) => /noto-serif-tc/i.test(n)),
      `沒有下載 Noto Serif TC：${names.join(", ")}`,
    ).toBe(true);
    const wrong = names.filter((n) => /noto-serif-sc|-(jp|ja|kr|ko)[-.]|japanese|korean/i.test(n));
    expect(wrong, `繁中筆記下載了不該下載的切片：${wrong.join(", ")}`).toEqual([]);
    // 上限：見 docs/ui/README.md 10.32（實測值與理由）
    expect(total, `woff2 總傳輸量 ${total} bytes`).toBeLessThan(FONT_BUDGET);
  });
});

/** 繁中筆記一頁的 woff2 上限（bytes）：見 docs/ui/README.md 10.32 */
const FONT_BUDGET = 1_500_000;
