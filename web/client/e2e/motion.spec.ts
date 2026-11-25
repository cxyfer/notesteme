// NFR-A11Y-3：prefers-reduced-motion 時沒有進行中的動畫與轉場；狀態不只靠顏色（形狀＋文字）；時間帶有文字版本。
// 灰階的「觀感」與螢幕閱讀器的朗讀仍是人工（docs/manual-acceptance.md 第 6 節）；這裡對 DOM 斷言形狀與文字，
// 並附上灰階截圖供人工對照。
import type { Page } from "@playwright/test";

import { expect, test, waitReady } from "./support/test";

/** 目前還在跑（running）的動畫與轉場 */
async function running(page: Page) {
  return page.evaluate(() =>
    document
      .getAnimations()
      .filter((a) => a.playState === "running")
      .map((a) => {
        const e = a.effect as KeyframeEffect | null;
        const t = e?.target as Element | null;
        const name =
          (a as CSSAnimation).animationName ?? (a as CSSTransition).transitionProperty ?? "";
        return `${a.constructor.name}(${name}) on ${t?.tagName.toLowerCase()}.${t?.getAttribute("class")?.split(" ")[0] ?? ""}`;
      }),
  );
}

/** 開抽屜、開對話框：會產生進場轉場的操作 */
async function openOverlays(page: Page) {
  await page.keyboard.press("n");
  await expect(page.getByRole("dialog", { name: "新增" })).toBeVisible();
}

test.describe("NFR-A11Y-3 reduced-motion", () => {
  test("對照組：沒有 reduce 時，打開抽屜有進場動畫（確認下一個測試不是空轉）", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "no-preference" });
    await page.goto("/library");
    await waitReady(page, "媒體庫");
    await page.keyboard.press("n");
    const seen = await page.evaluate(
      () =>
        new Promise<number>((resolve) => {
          let max = 0;
          const t0 = performance.now();
          const tick = () => {
            max = Math.max(max, document.getAnimations().length);
            if (performance.now() - t0 > 400) resolve(max);
            else requestAnimationFrame(tick);
          };
          tick();
        }),
    );
    expect(seen, "沒有 reduce 時抽屜沒有任何動畫：檢查方式失效").toBeGreaterThan(0);
  });

  for (const [name, path, heading] of [
    ["媒體庫", "/library", "媒體庫"],
    ["佇列", "/queue", "佇列"],
    ["總覽", "/", "總覽"],
  ] as const) {
    test(`reduce：${name} 載入後與打開抽屜後都沒有進行中的動畫`, async ({ page }) => {
      await page.emulateMedia({ reducedMotion: "reduce" });
      await page.goto(path);
      await waitReady(page, heading);
      // 動畫被縮成 0.01ms 一次：給一個 frame 結束
      await page.waitForTimeout(100);
      expect(await running(page)).toEqual([]);
      await openOverlays(page);
      const during = await page.evaluate(
        () =>
          new Promise<string[]>((resolve) => {
            const found = new Set<string>();
            const t0 = performance.now();
            const tick = () => {
              for (const a of document.getAnimations()) {
                const timing = a.effect?.getComputedTiming();
                // 0.01ms 的「動畫」等於沒有動畫；超過 1ms 的才算
                if (a.playState === "running" && Number(timing?.duration ?? 0) > 1) {
                  const t = (a.effect as KeyframeEffect).target as Element | null;
                  found.add(`${a.constructor.name} on ${t?.getAttribute("class")?.split(" ")[0]}`);
                }
              }
              if (performance.now() - t0 > 400) resolve([...found]);
              else requestAnimationFrame(tick);
            };
            tick();
          }),
      );
      expect(during, `reduce 時仍有動畫：${during.join("、")}`).toEqual([]);
    });
  }

  test("reduce：影音詳情（時間帶、字幕句、spinner）沒有進行中的動畫", async ({ page, env }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto(`/items/${env.noteItem}`);
    await waitReady(page, /^Video 1 about/);
    await page.waitForTimeout(100);
    expect(await running(page)).toEqual([]);
  });
});

test.describe("NFR-A11Y-3 狀態不只靠顏色", () => {
  test("每個狀態標籤都有圖示形狀與文字，不同狀態的形狀不同；時間帶有文字版本", async ({
    page,
    env,
  }, info) => {
    const shapes = new Map<string, Set<string>>();
    const problems: string[] = [];
    for (const path of ["/library", `/items/${env.failedItems[0]}?tab=stages`]) {
      await page.goto(path);
      await waitReady(page, path === "/library" ? "媒體庫" : /^Video \d+ about/);
      const badges = await page.locator(".status").evaluateAll((els) =>
        els.map((el) => ({
          status: el.getAttribute("data-status") ?? "",
          text: (el.textContent ?? "").trim(),
          icon: el.querySelector("svg")?.innerHTML.replace(/\s+/g, "") ?? "",
        })),
      );
      expect(badges.length, `${path} 沒有狀態標籤`).toBeGreaterThan(0);
      for (const b of badges) {
        if (!b.text) problems.push(`${path}：${b.status} 沒有文字`);
        if (!b.icon) problems.push(`${path}：${b.status} 沒有圖示形狀`);
        shapes.set(b.status, (shapes.get(b.status) ?? new Set()).add(b.icon));
      }
      // 時間帶（精簡版 role=img、完整版 role=slider）都有文字
      const bands = await page
        .locator("main [role=img], main [role=slider]")
        .evaluateAll((els) =>
          els.map((e) => e.getAttribute("aria-label") ?? e.getAttribute("aria-valuetext") ?? ""),
        );
      for (const label of bands) if (!label.trim()) problems.push(`${path}：時間帶沒有文字版本`);
      // 灰階截圖：人工對照用（design-language §6.4 的形狀在灰階下仍分得出）
      await page.addStyleTag({ content: "html { filter: grayscale(1) }" });
      await info.attach(`grayscale-${path.replace(/\W+/g, "_")}.png`, {
        body: await page.screenshot(),
        contentType: "image/png",
      });
    }
    // 不同狀態不共用同一個形狀
    const owner = new Map<string, string>();
    for (const [status, icons] of shapes) {
      for (const icon of icons) {
        const other = owner.get(icon);
        if (other && other !== status) problems.push(`狀態 ${other} 與 ${status} 的圖示形狀相同`);
        owner.set(icon, status);
      }
    }
    expect(shapes.size, "只看到一種狀態，檢查不出差異").toBeGreaterThan(1);
    expect(problems, problems.join("\n")).toEqual([]);
  });
});
