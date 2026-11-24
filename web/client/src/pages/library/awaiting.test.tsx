import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes, item } from "../../test/fixtures";
import { mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { toApiQuery, validateLibrarySearch } from "./filters";

describe("媒體庫：等你決定（DIG）與停住原因的篩選", () => {
  it("blocked 直接送 API 的 blocked（UI-8），不再換成近似的步驟條件", () => {
    const s = validateLibrarySearch({ blocked: "awaiting_user", status: "segment:done" });
    expect(s.blocked).toBe("awaiting_user");
    const q = toApiQuery(s);
    expect(q.blocked).toBe("awaiting_user");
    expect(q.status).toEqual(["segment:done"]);
    // 其他原因只驗格式（^[a-z_]{1,64}$）
    expect(validateLibrarySearch({ blocked: "budget_blocked" }).blocked).toBe("budget_blocked");
    expect(validateLibrarySearch({ blocked: "Budget" }).blocked).toBeUndefined();
    expect(validateLibrarySearch({ blocked: "a".repeat(65) }).blocked).toBeUndefined();
  });

  it("彙整的連結打開等你決定的檢視：送 blocked、提示逐列勾選（axe 無違規）", async () => {
    const api = mockApi({
      ...defaultRoutes(),
      "GET /api/items": () => ({ items: [item(1), item(2)], total: 2 }),
    });
    signIn();
    const { container } = renderRoute("/library?blocked=awaiting_user");
    expect(await screen.findByText(/勾選要繼續的影音/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "等你決定" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    expect(screen.queryByRole("button", { name: /繼續處理已載入的/ })).toBeNull();
    const q = api.calls("GET", "/api/items")[0]!.url.searchParams;
    expect(q.get("blocked")).toBe("awaiting_user");
    expect(q.getAll("status")).toEqual([]);
    await expectNoA11yViolations(container);
  });

  it("其他停住原因顯示通用的條件標籤", async () => {
    const api = mockApi({
      ...defaultRoutes(),
      "GET /api/items": () => ({ items: [item(1)], total: 1 }),
    });
    signIn();
    renderRoute("/library?blocked=budget_blocked");
    expect(await screen.findByText("停住的原因：budget_blocked")).toBeTruthy();
    expect(screen.getByRole("button", { name: "等你決定" }).getAttribute("aria-pressed")).toBe(
      "false",
    );
    expect(screen.queryByText(/勾選要繼續的影音/)).toBeNull();
    expect(api.calls("GET", "/api/items")[0]!.url.searchParams.get("blocked")).toBe(
      "budget_blocked",
    );
  });
});
