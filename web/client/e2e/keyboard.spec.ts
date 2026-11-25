// NFR-A11Y-2：只用鍵盤完成主要操作；焦點框依 UI 規格（:focus-visible 的 outline 2px solid var(--focus)、offset 2px）；
// 可點擊目標 ≥ 24×24px 由 a11y.spec.ts 的 axe target-size 檢查。
// 螢幕閱讀器的朗讀與完整的逐頁走訪仍是人工（docs/manual-acceptance.md 第 5 節）。
import type { Page } from "@playwright/test";

import { expect, test, waitReady } from "./support/test";

/** 目前焦點元素的描述與焦點框 */
async function focused(page: Page) {
  return page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    if (!el || el === document.body) return null;
    const s = getComputedStyle(el);
    const focus = getComputedStyle(document.documentElement).getPropertyValue("--focus").trim();
    const r = el.getBoundingClientRect();
    // 外框不能被會裁切的祖先（overflow 不是 visible）裁掉：外框的範圍＝元素加上 width＋offset
    const grow = parseFloat(s.outlineWidth) + Math.max(0, parseFloat(s.outlineOffset));
    let clippedBy = "";
    for (let p = el.parentElement; p && p !== document.documentElement; p = p.parentElement) {
      const ps = getComputedStyle(p);
      if (ps.overflowX === "visible" && ps.overflowY === "visible") continue;
      const b = p.getBoundingClientRect();
      // 捲動容器裡被捲出可視範圍的元素不算（Tab 會先把它捲進來）
      if (r.bottom <= b.top || r.top >= b.bottom || r.right <= b.left || r.left >= b.right)
        continue;
      const cut =
        (ps.overflowX !== "visible" &&
          (r.left - grow < b.left - 0.5 || r.right + grow > b.right + 0.5)) ||
        (ps.overflowY !== "visible" &&
          (r.top - grow < b.top - 0.5 || r.bottom + grow > b.bottom + 0.5));
      if (cut) {
        clippedBy = `${p.tagName.toLowerCase()}.${String(p.className).split(" ")[0]}`;
        break;
      }
    }
    return {
      clippedBy,
      desc: `${el.tagName.toLowerCase()}${el.id ? `#${el.id}` : ""}.${String(el.className).split(" ")[0]} 「${(el.getAttribute("aria-label") ?? el.textContent ?? "").trim().slice(0, 30)}」`,
      visible: el.matches(":focus-visible"),
      outlineStyle: s.outlineStyle,
      outlineWidth: parseFloat(s.outlineWidth),
      outlineOffset: parseFloat(s.outlineOffset),
      outlineColor: s.outlineColor,
      focusToken: focus,
      inDialog: !!el.closest("[role=dialog]"),
      onScreen: r.width > 0 && r.height > 0,
      tag: el.tagName,
      id: el.id,
    };
  });
}

/** 把 CSS 顏色字串正規化成 rgb()，比對 outline 的顏色與 --focus token */
async function sameColor(page: Page, a: string, b: string) {
  return page.evaluate(
    ([x, y]) => {
      const probe = (c: string) => {
        const d = document.createElement("div");
        d.style.color = c;
        document.body.append(d);
        const v = getComputedStyle(d).color;
        d.remove();
        return v;
      };
      return probe(x!) === probe(y!);
    },
    [a, b],
  );
}

/** Tab 走過頁面上前 n 個停駐點，每個都要有規格的焦點框 */
async function walkFocusRings(page: Page, n: number) {
  const problems: string[] = [];
  const seen = new Set<string>();
  for (let i = 0; i < n; i++) {
    await page.keyboard.press("Tab");
    const f = await focused(page);
    if (!f) continue;
    if (seen.has(f.desc)) continue;
    seen.add(f.desc);
    if (!f.onScreen) continue; // skip link 以外看不到的元素不會拿到焦點；看不到就不檢查外框
    // 嵌入的播放器（YouTube／Bilibili 的跨來源 iframe）：焦點進入第三方的文件，外框由播放器自己畫；e2e 也擋下了它
    if (f.tag === "IFRAME") continue;
    const ok =
      f.visible &&
      f.outlineStyle === "solid" &&
      f.outlineWidth >= 2 &&
      f.outlineOffset >= 2 &&
      (await sameColor(page, f.outlineColor, f.focusToken));
    if (ok && f.clippedBy) {
      problems.push(`${f.desc}：焦點框被 ${f.clippedBy} 裁掉`);
      continue;
    }
    if (!ok) {
      problems.push(
        `${f.desc}：focus-visible=${f.visible} outline=${f.outlineStyle} ${f.outlineWidth}px offset ${f.outlineOffset}px ${f.outlineColor}（--focus ${f.focusToken}）`,
      );
    }
  }
  return { problems, stops: seen.size };
}

test.describe("NFR-A11Y-2 鍵盤", () => {
  test("skip link：第一個 Tab 停在「跳到主要內容」，Enter 後焦點在 <main>", async ({ page }) => {
    await page.goto("/library");
    await waitReady(page, "媒體庫");
    await page.keyboard.press("Tab");
    const f = await focused(page);
    expect(f?.desc).toContain("跳到主要內容");
    expect(f?.visible).toBe(true);
    await expect(page.getByRole("link", { name: "跳到主要內容" })).toBeInViewport();
    await page.keyboard.press("Enter");
    expect((await focused(page))?.id).toBe("main");
  });

  test("用鍵盤換頁（G 然後 Q、Tab 到導覽連結 Enter）：焦點移到新頁的 <main>", async ({ page }) => {
    await page.goto("/library");
    await waitReady(page, "媒體庫");
    await page.keyboard.press("g");
    await page.keyboard.press("q");
    await waitReady(page, "佇列");
    expect((await focused(page))?.id).toBe("main");
    // Tab 到側邊導覽的「訂閱」，Enter
    for (let i = 0; i < 30; i++) {
      await page.keyboard.press("Tab");
      const f = await focused(page);
      if (f?.desc.includes("訂閱") && f.tag === "A") break;
    }
    expect((await focused(page))?.desc).toContain("訂閱");
    await page.keyboard.press("Enter");
    await waitReady(page, "訂閱");
    expect((await focused(page))?.id).toBe("main");
  });

  test("新增抽屜：Enter 開啟、焦點鎖在裡面、Esc 關閉、焦點回到「新增」", async ({ page }) => {
    await page.goto("/library");
    await waitReady(page, "媒體庫");
    const add = page.locator("nav.nav").getByRole("button", { name: /新增/ });
    for (let i = 0; i < 10; i++) {
      await page.keyboard.press("Tab");
      if (await add.evaluate((el) => el === document.activeElement)) break;
    }
    await expect(add).toBeFocused();
    await page.keyboard.press("Enter");
    const drawer = page.getByRole("dialog", { name: "新增" });
    await expect(drawer).toBeVisible();
    expect((await focused(page))?.inDialog).toBe(true);
    await page.keyboard.type("https://www.youtube.com/watch?v=e2eKeybd001");
    await expect(drawer.getByRole("region", { name: "YouTube 影片" })).toBeVisible();
    const escaped: string[] = [];
    for (let i = 0; i < 40; i++) {
      await page.keyboard.press(i % 7 === 6 ? "Shift+Tab" : "Tab");
      const f = await focused(page);
      if (!f?.inDialog) escaped.push(f?.desc ?? "body");
    }
    expect(escaped, `焦點離開了抽屜：${escaped.join("、")}`).toEqual([]);
    await page.keyboard.press("Escape");
    await expect(drawer).toBeHidden();
    await expect(add).toBeFocused();
  });

  test("快捷鍵說明（?）：對話框焦點鎖定、Esc 關閉後焦點歸還", async ({ page }) => {
    await page.goto("/queue");
    await waitReady(page, "佇列");
    await page.keyboard.press("Tab"); // skip link
    await page.keyboard.press("?");
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    for (let i = 0; i < 15; i++) {
      await page.keyboard.press("Tab");
      expect((await focused(page))?.inDialog, `第 ${i + 1} 次 Tab 離開了對話框`).toBe(true);
    }
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    expect((await focused(page))?.desc).toContain("跳到主要內容");
  });

  for (const [name, path, heading, ready] of [
    ["媒體庫", "/library", "媒體庫", undefined],
    ["佇列", "/queue", "佇列", "下載"],
    ["設定（服務）", "/settings/services", "設定", undefined],
    ["Prompt 編輯", "/prompts/correct-default", "correct-default", undefined],
  ] as const) {
    test(`焦點框：${name} 的每個停駐點都有 2px 的 --focus 外框`, async ({ page }) => {
      await page.goto(path);
      await waitReady(page, heading, ready);
      const { problems, stops } = await walkFocusRings(page, 60);
      expect(stops, "Tab 沒有走到任何元素").toBeGreaterThan(5);
      expect(problems, problems.join("\n")).toEqual([]);
    });
  }

  test("焦點框：影音詳情（字幕句、時間帶、分頁）", async ({ page, env }) => {
    await page.goto(`/items/${env.noteItem}`);
    await waitReady(page, /^Video 1 about/);
    const { problems, stops } = await walkFocusRings(page, 80);
    expect(stops).toBeGreaterThan(10);
    expect(problems, problems.join("\n")).toEqual([]);
  });

  for (const theme of ["light", "dark"] as const) {
    test(`焦點框的顏色跟著主題（${theme}）`, async ({ page }) => {
      await page.emulateMedia({ colorScheme: theme });
      await page.goto("/library");
      await waitReady(page, "媒體庫");
      await page.keyboard.press("Tab");
      const f = await focused(page);
      // 淺色是 Ink，深色是 Cue（docs/ui/README.md §6）
      const expected = theme === "light" ? "#1f2a3a" : "#f3ca40";
      expect(await sameColor(page, f!.outlineColor, expected), `${f!.outlineColor}`).toBe(true);
    });
  }
});
