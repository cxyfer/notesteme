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

  it("進行中的告警列出訊息與持續時間，旁邊列出最近沒有送出的通知（OPS 的 @ui 場景）", async () => {
    const hourAgo = new Date(Date.now() - 3 * 3600_000).toISOString();
    const old = new Date(Date.now() - 3 * 86400_000).toISOString();
    const api = overviewApi({
      "GET /api/system": () => ({
        alerts: [
          {
            kind: "disk_usage",
            key: "/data",
            message: "/data 的磁碟用量是 93%，超過 90%。",
            since: hourAgo,
          },
        ],
        backup: { last_ok_at: null, last_error: null, next_run_at: null },
      }),
      "GET /api/notifications": () => ({
        notifications: [
          {
            id: "n1",
            notifier: "phone",
            event: "alert",
            state: "dropped",
            attempts: 6,
            last_error: "通知端點回應 HTTP 503。",
            created_at: hourAgo,
            sent_at: null,
          },
          // 超過 24 小時的不列出
          {
            id: "n0",
            notifier: "phone",
            event: "item.done",
            state: "dropped",
            attempts: 1,
            last_error: "連線失敗。",
            created_at: old,
            sent_at: null,
          },
        ],
      }),
    });
    signIn();
    const { container } = renderRoute("/");
    const sec = (await screen.findByRole("heading", { name: "系統告警" })).closest("section")!;
    expect(within(sec).getByText("磁碟空間不足")).toBeTruthy();
    expect(within(sec).getByText("/data 的磁碟用量是 93%，超過 90%。")).toBeTruthy();
    expect(within(sec).getByText(/已持續 3 小時/)).toBeTruthy();
    expect(
      await within(sec).findByRole("heading", { name: "最近 24 小時有 1 則通知沒有送出" }),
    ).toBeTruthy();
    expect(
      within(sec).getByText(/phone 的「系統告警」通知沒有送出。 通知端點回應 HTTP 503。/),
    ).toBeTruthy();
    expect(within(sec).queryByText(/連線失敗/)).toBeNull();
    expect(api.calls("GET", "/api/notifications")[0]!.url.searchParams.get("state")).toBe(
      "dropped",
    );
    await expectNoA11yViolations(container);
  });

  it("沒有告警、也沒有沒送出的通知（或讀不到）時不顯示告警區塊", async () => {
    overviewApi({ "GET /api/system": () => apiError(403, "forbidden", "只有 owner") });
    signIn();
    renderRoute("/");
    await screen.findByRole("heading", { name: "現在" });
    expect(screen.queryByRole("heading", { name: "系統告警" })).toBeNull();
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
