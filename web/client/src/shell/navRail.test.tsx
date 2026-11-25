// 768–1279px 的收合導覽（docs/ui/README.md §8、design-language.md §5.1）：64px 窄側欄只剩圖示。
// jsdom 不套用樣式表：渲染的差異用 matchMedia 模擬，版面規則直接讀 base.css 斷言。
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { readFileSync } from "node:fs";
import { afterEach, describe, expect, it, vi } from "vitest";

import { expectNoA11yViolations } from "../test/axe";
import { defaultRoutes } from "../test/fixtures";
import { mockApi } from "../test/mockApi";
import { renderRoute, signIn } from "../test/render";
import { NARROW, RAIL } from "./media";

/** 模擬視窗寬度：只有 match 列出的媒體查詢成立 */
function viewport(match: string[]) {
  vi.stubGlobal(
    "matchMedia",
    (query: string) =>
      ({
        matches: match.includes(query),
        media: query,
        addEventListener: () => {},
        removeEventListener: () => {},
      }) as unknown as MediaQueryList,
  );
}

afterEach(() => vi.unstubAllGlobals());

const PAGES = [
  "總覽",
  "媒體庫",
  "佇列",
  "訂閱",
  "Prompt 庫",
  "處理方案",
  "字幕樣式",
  "筆記模板",
  "成本",
  "設定",
];

describe("側邊導覽的圖示", () => {
  it("每一項都有 aria-hidden 的圖示，連結名稱是完整的頁面名稱；目前頁面有 aria-current", async () => {
    mockApi(defaultRoutes());
    signIn();
    renderRoute("/library");
    const nav = await screen.findByRole("navigation", { name: "主要導覽" });
    for (const name of PAGES) {
      const link = within(nav).getByRole("link", { name });
      const svg = link.querySelector("svg.nav__icon");
      expect(svg?.getAttribute("aria-hidden"), name).toBe("true");
      expect(link.querySelector(".nav__label")?.textContent).toBe(name);
    }
    expect(within(nav).getByRole("link", { name: "媒體庫" }).getAttribute("aria-current")).toBe(
      "page",
    );
    // 品牌字：完整名稱在 accessible name，縮寫 aria-hidden
    expect(within(nav).getByRole("link", { name: "notesteme" })).toBeTruthy();
    expect(within(nav).getByRole("button", { name: /新增/ }).querySelector("svg")).toBeTruthy();
  });

  it("1280px 以上：偏好設定與登出直接放在導覽底部", async () => {
    viewport([]);
    mockApi(defaultRoutes());
    signIn();
    renderRoute("/library");
    const nav = await screen.findByRole("navigation", { name: "主要導覽" });
    expect(within(nav).getByRole("button", { name: "登出" })).toBeTruthy();
    expect(within(nav).queryByRole("button", { name: "偏好設定與登出" })).toBeNull();
  });
});

describe("768–1279px 的窄側欄", () => {
  it("偏好設定與登出收進一個圖示按鈕，打開對話框（axe 無違規）", async () => {
    viewport([RAIL]);
    mockApi(defaultRoutes());
    signIn();
    const { container } = renderRoute("/library");
    const user = userEvent.setup();
    const nav = await screen.findByRole("navigation", { name: "主要導覽" });
    expect(screen.queryByRole("navigation", { name: "分頁列" })).toBeNull();
    expect(within(nav).queryByRole("button", { name: "登出" })).toBeNull();
    expect(within(nav).getAllByRole("link").length).toBeGreaterThanOrEqual(PAGES.length);
    await expectNoA11yViolations(container);
    const account = within(nav).getByRole("button", { name: "偏好設定與登出" });
    expect(account.getAttribute("aria-haspopup")).toBe("dialog");
    expect(account.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
    await user.click(account);
    const dialog = await screen.findByRole("dialog", { name: "偏好設定與登出" });
    expect(within(dialog).getByRole("button", { name: "登出" })).toBeTruthy();
    expect(within(dialog).getByLabelText("主題")).toBeTruthy();
    await expectNoA11yViolations(dialog);
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("767px 以下仍是底部分頁列，不渲染側邊導覽", async () => {
    viewport([NARROW]);
    mockApi(defaultRoutes());
    signIn();
    renderRoute("/library");
    await screen.findByRole("navigation", { name: "分頁列" });
    expect(screen.queryByRole("navigation", { name: "主要導覽" })).toBeNull();
  });
});

describe("窄側欄的 CSS（base.css）", () => {
  const css = readFileSync("src/styles/base.css", "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
  const start = css.indexOf("@media (min-width: 768px) and (max-width: 1279px) {");
  const block = css.slice(start, css.indexOf("\n}\n", start));

  it("斷點與 media.ts 一致；寬 64px", () => {
    expect(start).toBeGreaterThan(-1);
    expect(RAIL).toBe("(min-width: 768px) and (max-width: 1279px)");
    expect(block).toMatch(/grid-template-columns:\s*64px minmax\(0, 1fr\)/);
  });

  it("目標至少 24×24px（WCAG 2.2 的 2.5.8）", () => {
    const target = /\.nav \.nav__item,\s*\.nav \.nav__add \{([^}]*)\}/.exec(block)?.[1] ?? "";
    expect(Number(/\bwidth:\s*(\d+)px/.exec(target)?.[1])).toBeGreaterThanOrEqual(24);
    expect(Number(/min-height:\s*(\d+)px/.exec(target)?.[1])).toBeGreaterThanOrEqual(24);
  });

  it("名稱平常裁成 1px（仍在無障礙樹中，不用 display/visibility），hover 與鍵盤焦點時顯示", () => {
    const hidden = /\.nav \.nav__label \{([^}]*)\}/.exec(block)?.[1] ?? "";
    expect(hidden).toMatch(/clip: rect\(0 0 0 0\)/);
    expect(block).not.toMatch(/\.nav__label[^{]*\{[^}]*(display:\s*none|visibility:\s*hidden)/);
    expect(block).toMatch(/\.nav \.nav__item:hover > \.nav__label/);
    expect(block).toMatch(/\.nav \.nav__item:focus-visible > \.nav__label/);
  });

  it("沒有展開動畫（prefers-reduced-motion 下也不會動）", () => {
    expect(block).not.toMatch(/transition|animation/);
  });
});
