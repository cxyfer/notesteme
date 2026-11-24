import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../test/axe";
import { defaultRoutes, item } from "../test/fixtures";
import { mockApi } from "../test/mockApi";
import { renderRoute, signIn } from "../test/render";

async function openPalette(routes = {}) {
  const api = mockApi({ ...defaultRoutes(), ...routes });
  signIn();
  const r = renderRoute("/library");
  await screen.findByRole("heading", { level: 1, name: "媒體庫" });
  const user = userEvent.setup();
  await user.keyboard("{Control>}k{/Control}");
  const dialog = await screen.findByRole("dialog", { name: "指令面板" });
  return { ...r, api, user, dialog, input: within(dialog).getByRole("combobox") };
}

describe("指令面板（README §3.1，UI-8）", () => {
  it("Ctrl K 打開；列出頁面與動作，佇列依狀態只出現暫停或繼續（axe 無違規）", async () => {
    const { container, dialog, input } = await openPalette();
    expect(document.activeElement).toBe(input);
    expect(within(dialog).getByRole("option", { name: "垃圾桶" })).toBeTruthy();
    expect(await within(dialog).findByRole("option", { name: "暫停佇列" })).toBeTruthy();
    expect(within(dialog).queryByRole("option", { name: "繼續處理" })).toBeNull();
    expect(input.getAttribute("aria-activedescendant")).toBe(
      within(dialog).getAllByRole("option")[0]!.id,
    );
    await expectNoA11yViolations(container.ownerDocument.body);
  });

  it("輸入文字過濾頁面；↓ 與 Enter 前往選中的頁面", async () => {
    const { router, user, dialog } = await openPalette();
    await user.keyboard("設定");
    // 設定以外的頁面都被過濾掉；影音的搜尋另外列在最上面
    await waitFor(() =>
      expect(
        within(dialog)
          .getAllByRole("option")
          .map((o) => o.textContent),
      ).toContain("設定"),
    );
    expect(within(dialog).queryByRole("option", { name: "佇列" })).toBeNull();
    const options = within(dialog).getAllByRole("option");
    const target = options.findIndex((o) => o.textContent === "設定");
    for (let i = 0; i < target; i++) await user.keyboard("{ArrowDown}");
    await user.keyboard("{Enter}");
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/services"));
    expect(screen.queryByRole("dialog", { name: "指令面板" })).toBeNull();
  });

  it("搜尋影音標題，並提供在字幕內容中搜尋", async () => {
    const { api, router, user, dialog } = await openPalette({
      "GET /api/items": () => ({ items: [item(3, { title: "Agile 的誕生" })], total: 1 }),
    });
    await user.keyboard("Agile");
    const hit = await within(dialog).findByRole("option", { name: /Agile 的誕生/ });
    expect(
      api.calls("GET", "/api/items").some((c) => c.url.searchParams.get("q") === "Agile"),
    ).toBe(true);
    expect(within(dialog).getByRole("option", { name: "在字幕內容中搜尋「Agile」" })).toBeTruthy();
    await user.click(hit);
    await waitFor(() => expect(router.state.location.pathname).toBe("/items/item-3"));
  });

  it("執行動作：暫停佇列並顯示結果", async () => {
    let paused = false;
    const { api, user, dialog } = await openPalette({
      "POST /api/queue/pause": () => {
        paused = true;
        return { paused: true, lanes: [], tasks: [] };
      },
    });
    await user.click(await within(dialog).findByRole("option", { name: "暫停佇列" }));
    expect(await screen.findByText("已暫停佇列。")).toBeTruthy();
    expect(paused).toBe(true);
    expect(api.calls("POST", "/api/queue/pause")).toHaveLength(1);
  });

  it("「新增」關閉面板後打開新增抽屜，焦點在抽屜裡", async () => {
    const { user, dialog } = await openPalette();
    await user.click(within(dialog).getByRole("option", { name: "新增" }));
    const drawer = await screen.findByRole("dialog", { name: "新增" });
    await waitFor(() => expect(drawer.contains(document.activeElement)).toBe(true));
    // 面板卸載時延遲還原的焦點不會把焦點搶回去
    await new Promise((r) => setTimeout(r, 50));
    expect(drawer.contains(document.activeElement)).toBe(true);
    expect(screen.queryByRole("dialog", { name: "指令面板" })).toBeNull();
  });
});
