import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes } from "../../test/fixtures";
import { apiError, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { emptyOverview, overview } from "../../test/ui7Fixtures";

function overviewApi(extra: Record<string, MockHandler> = {}) {
  return mockApi({ ...defaultRoutes(), ...extra });
}

describe("總覽", () => {
  it("現在、需要你處理、今天、底部的本月花費（axe 無違規）", async () => {
    const api = overviewApi();
    signIn();
    const { container } = renderRoute("/");
    await screen.findByRole("heading", { level: 1, name: "總覽" });
    const now = (await screen.findByRole("heading", { name: "現在" })).closest("section")!;
    expect(within(now).getByText("處理中 4 部")).toBeTruthy();
    expect(within(now).getByRole("link", { name: "影片 1" })).toBeTruthy();
    expect(within(now).getByText("校正 9/16")).toBeTruthy();
    // 超過 3 部時「還有 N 部」連到佇列
    expect(
      within(now).getByRole("link", { name: "還有 3 部，查看佇列" }).getAttribute("href"),
    ).toBe("/queue");
    const needs = screen.getByRole("heading", { name: "需要你處理" }).closest("section")!;
    expect(within(needs).getByText(/本月預算已用完/)).toBeTruthy();
    expect(within(needs).getByRole("button", { name: "調整預算" })).toBeTruthy();
    expect(within(needs).getByRole("button", { name: "查看成本" })).toBeTruthy();
    const today = screen.getByRole("heading", { name: "今天" }).closest("section")!;
    const rows = within(today).getAllByRole("listitem");
    expect(rows[0]!.textContent).toMatch(/新增了「影片 1」/);
    expect(screen.getByText(/本月花費 US\$3\.42，預算 US\$20\.00/)).toBeTruthy();
    // 待確認術語的提醒
    expect(screen.getByText("有 3 個術語等你確認。")).toBeTruthy();
    // 時區由瀏覽器帶上（L-M8）
    expect(api.calls("GET", "/api/overview")[0]!.url.searchParams.has("tz")).toBe(true);
    await expectNoA11yViolations(container);
  });

  it("「調整預算」打開設定的預算分區", async () => {
    overviewApi();
    signIn();
    const { router } = renderRoute("/");
    await userEvent.click(await screen.findByRole("button", { name: "調整預算" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/budget"));
  });

  it("沒有需要處理的項目時整個區塊隱藏；媒體庫是空的時顯示空狀態", async () => {
    overviewApi({
      "GET /api/overview": () => emptyOverview(),
      "GET /api/items": () => ({ items: [], total: 0 }),
    });
    signIn();
    renderRoute("/");
    expect(await screen.findByText(/還沒有任何影音/)).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "需要你處理" })).toBeNull();
    expect(screen.getByText("目前沒有正在處理的影音。")).toBeTruthy();
  });

  it("不認識的時區（422）時不帶 tz 重讀", async () => {
    const api = overviewApi({
      "GET /api/overview": (req) =>
        req.url.searchParams.has("tz")
          ? apiError(422, "invalid_request", "不認識的時區")
          : overview({ notices: [] }),
    });
    signIn();
    renderRoute("/");
    expect(await screen.findByText("超出預算的影片")).toBeTruthy();
    expect(api.calls("GET", "/api/overview").length).toBeGreaterThanOrEqual(2);
  });
});
