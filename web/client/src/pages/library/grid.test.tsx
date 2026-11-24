import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes, item } from "../../test/fixtures";
import { mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { hasFilters, toApiQuery, validateLibrarySearch } from "./filters";

describe("媒體庫的「格」檢視（pages.md §3，UI-8）", () => {
  it("view=grid 存在網址，不算篩選條件、不送 API", () => {
    const s = validateLibrarySearch({ view: "grid" });
    expect(s).toEqual({ view: "grid" });
    expect(hasFilters(s)).toBe(false);
    expect(toApiQuery(s)).not.toHaveProperty("view");
    expect(validateLibrarySearch({ view: "table" })).toEqual({});
  });

  it("切換成格：縮圖網格、沒有多選；切回列（axe 無違規）", async () => {
    const api = mockApi({
      ...defaultRoutes(),
      "GET /api/items": () => ({ items: [item(1), item(2)], total: 2 }),
    });
    signIn();
    const { router, container } = renderRoute("/library");
    const user = userEvent.setup();
    const views = await screen.findByRole("group", { name: "顯示方式" });
    await user.click(within(views).getByRole("button", { name: "格" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ view: "grid" }));
    const grid = await screen.findByRole("list", { name: "影音列表" });
    expect(within(grid).getByRole("link", { name: "影片 2" }).getAttribute("href")).toBe(
      "/items/item-2",
    );
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(within(views).getByRole("button", { name: "格" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    await expectNoA11yViolations(container);
    // 切換顯示方式不重新讀取列表
    expect(api.calls("GET", "/api/items")).toHaveLength(1);
    await user.click(within(views).getByRole("button", { name: "列" }));
    // 列檢視每列另讀 detail（fixture 的 detail 標題都是「影片 1」）
    expect(await screen.findAllByRole("checkbox", { name: /^選取「/ })).toHaveLength(2);
  });
});
