import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { subscription, subscriptionEntries } from "../../test/m4Fixtures";
import { apiError, json, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { EMPTY_FORM, createBody, formFromView, patchBody } from "./subModel";

describe("訂閱：模型", () => {
  it("片長用分鐘輸入、送出秒；空白不送", () => {
    const body = createBody({
      ...EMPTY_FORM,
      url: " https://x.test/feed ",
      minMinutes: "5",
      maxMinutes: "",
    });
    expect(body).toEqual({
      url: "https://x.test/feed",
      schedule: "6h",
      backfill: { mode: "none" },
      filters: { exclude_shorts: true, live: "wait", min_duration_s: 300 },
    });
  });

  it("PATCH 只送改過的欄位；處理方案改回預設送空字串；沒有差異是 undefined", () => {
    const before = formFromView(subscription("s1", { profile: "standard" }));
    expect(patchBody(before, { ...before })).toBeUndefined();
    expect(patchBody(before, { ...before, schedule: "12h" })).toEqual({ schedule: "12h" });
    expect(patchBody(before, { ...before, profile: "" })).toEqual({ profile: "" });
    // 回補範圍沒有改就不送（送出會觸發重新評估，SUB-R3）
    const p = patchBody(before, { ...before, titleExclude: "抽獎" });
    expect(p).toEqual({ filters: { exclude_shorts: true, live: "wait", title_exclude: "抽獎" } });
  });
});

function subsApi(extra: Record<string, MockHandler> = {}) {
  return mockApi({
    "GET /api/subscriptions": () => ({
      subscriptions: [
        subscription("s1", { new_items: 3 }),
        subscription("s2", {
          title: "",
          url: "https://feeds.example.com/pod.xml",
          platform: "podcast",
          kind: "feed",
          enabled: false,
          next_check_at: null,
          last_error: "訂閱來源已不存在（HTTP 404）。",
          last_error_class: "permanent",
        }),
      ],
    }),
    "GET /api/subscriptions/{id}": (req) =>
      req.params.id === "s1" ? subscription("s1") : apiError(404, "not_found", "找不到"),
    "GET /api/subscriptions/{id}/entries": () => subscriptionEntries(),
    "GET /api/profiles": () => ({ profiles: [] }),
    ...extra,
  });
}

describe("訂閱：列表", () => {
  it("列出訂閱、停用與錯誤（axe 無違規）", async () => {
    subsApi();
    signIn();
    const { container } = renderRoute("/subscriptions");
    await screen.findByRole("heading", { level: 1, name: "訂閱" });
    expect(await screen.findByRole("link", { name: "码农高天" })).toBeTruthy();
    // 沒有標題時顯示網址
    expect(screen.getByRole("link", { name: "https://feeds.example.com/pod.xml" })).toBeTruthy();
    expect(screen.getByText("已停用")).toBeTruthy();
    expect(screen.getByText(/訂閱來源已不存在/)).toBeTruthy();
    // SUB 的 @ui 場景：平台、檢查頻率、上次與下次檢查、處理方案與新內容（SUB-R14）
    const headers = screen.getAllByRole("columnheader").map((h) => h.textContent);
    expect(headers).toEqual([
      "頻道",
      "平台",
      "檢查頻率",
      "上次檢查",
      "下次檢查",
      "處理方案",
      "新內容",
      "狀態",
    ]);
    const row = screen.getByRole("link", { name: "码农高天" }).closest("tr")!;
    expect(within(row).getAllByRole("cell")[5]!.textContent).toBe("3");
    await expectNoA11yViolations(container);
  });

  it("沒有訂閱時顯示空狀態與「新增訂閱」", async () => {
    mockApi({ "GET /api/subscriptions": () => ({ subscriptions: [] }) });
    signIn();
    renderRoute("/subscriptions");
    expect(await screen.findByText(/還沒有訂閱/)).toBeTruthy();
  });

  it("新增：按過預覽後才會連外，之後依設定 debounce 更新；新增後到詳情頁", async () => {
    const previews: unknown[] = [];
    const api = subsApi({
      "POST /api/subscriptions/preview": (req) => {
        previews.push(req.body);
        return {
          would_process: [
            {
              ref: "yt:new",
              title: "新內容",
              published_at: "2026-11-21T00:00:00Z",
              duration_s: 300,
            },
          ],
          would_skip: [
            {
              ref: "yt:old",
              title: "舊內容",
              published_at: "2026-10-01T00:00:00Z",
              duration_s: 600,
              reason: "before_subscription",
            },
          ],
          truncated: false,
        };
      },
      "POST /api/subscriptions": () => json(201, subscription("s1")),
    });
    signIn();
    const user = userEvent.setup();
    const { router } = renderRoute("/subscriptions");
    await user.click(await screen.findByRole("button", { name: "新增訂閱" }));
    const dialog = await screen.findByRole("dialog");
    // 網址還不合法時不能預覽
    const run = within(dialog).getByRole("button", { name: "試跑預覽" });
    expect((run as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(within(dialog).getByLabelText("來源"), {
      target: { value: "https://www.youtube.com/@codinggaotian" },
    });
    expect(previews).toHaveLength(0);
    expect(within(dialog).getByText(/會連到來源網站/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "試跑預覽" }));
    expect(await within(dialog).findByText("新內容", {}, { timeout: 3000 })).toBeTruthy();
    expect(within(dialog).getByText("原因：訂閱之前的內容")).toBeTruthy();
    expect(previews).toHaveLength(1);
    // 連續修改只送一次，內容是最後一次
    fireEvent.change(within(dialog).getByLabelText("標題不包含"), { target: { value: "抽" } });
    fireEvent.change(within(dialog).getByLabelText("標題不包含"), { target: { value: "抽獎" } });
    await waitFor(() => expect(previews).toHaveLength(2), { timeout: 3000 });
    expect(previews[1]).toMatchObject({ filters: { title_exclude: "抽獎" } });

    await user.click(within(dialog).getByRole("button", { name: "新增訂閱" }));
    await waitFor(() => expect(api.calls("POST", "/api/subscriptions")).toHaveLength(1));
    await waitFor(() => expect(router.state.location.pathname).toBe("/subscriptions/s1"));
  });

  it("預覽 429 顯示等待秒數，不自動重試", async () => {
    const api = subsApi({
      "POST /api/subscriptions/preview": () =>
        apiError(429, "rate_limited", "太頻繁", { retry_after_s: 42 }),
    });
    signIn();
    const user = userEvent.setup();
    renderRoute("/subscriptions");
    await user.click(await screen.findByRole("button", { name: "新增訂閱" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("來源"), {
      target: { value: "https://feeds.example.com/pod.xml" },
    });
    await user.click(within(dialog).getByRole("button", { name: "試跑預覽" }));
    expect(await within(dialog).findByText(/請在 42 秒後再試/, {}, { timeout: 3000 })).toBeTruthy();
    expect(api.calls("POST", "/api/subscriptions/preview")).toHaveLength(1);
  });

  it("重複的訂閱提供已有訂閱的連結", async () => {
    subsApi({
      "POST /api/subscriptions": () =>
        apiError(409, "duplicate_subscription", "已經訂閱了這個來源。", { subscription_id: "s1" }),
    });
    signIn();
    const user = userEvent.setup();
    renderRoute("/subscriptions");
    await user.click(await screen.findByRole("button", { name: "新增訂閱" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("來源"), {
      target: { value: "https://www.youtube.com/@codinggaotian" },
    });
    await user.click(within(dialog).getByRole("button", { name: "新增訂閱" }));
    expect(await within(dialog).findByText("已經訂閱了這個來源。")).toBeTruthy();
    expect(within(dialog).getByRole("link", { name: "開啟已有的訂閱" })).toBeTruthy();
  });

  it("匯入：送 multipart，列出新增、重複與失敗的列", async () => {
    const api = subsApi({
      "POST /api/subscriptions/import": () => ({
        created: 3,
        duplicates: ["https://a.test/feed"],
        failed: [{ line: 7, url: "ftp://x", message: "網址必須是 http 或 https。" }],
      }),
    });
    signIn();
    const user = userEvent.setup();
    renderRoute("/subscriptions");
    await user.click(await screen.findByRole("button", { name: "匯入" }));
    const dialog = await screen.findByRole("dialog");
    const file = new File(["<opml/>"], "subs.opml", { type: "text/xml" });
    await user.upload(within(dialog).getByLabelText("匯入檔"), file);
    await user.click(within(dialog).getByRole("button", { name: "匯入" }));
    expect(await within(dialog).findByText("已新增 3 個訂閱。")).toBeTruthy();
    expect(within(dialog).getByText(/第 7 行/)).toBeTruthy();
    const init = api.calls("POST", "/api/subscriptions/import")[0]!.init;
    expect(init.body).toBeInstanceOf(FormData);
  });
});

describe("訂閱：詳情", () => {
  it("顯示設定與條目（axe 無違規）", async () => {
    subsApi();
    signIn();
    const { container } = renderRoute("/subscriptions/s1");
    await screen.findByRole("heading", { level: 1, name: "码农高天" });
    expect(await screen.findByRole("link", { name: "新影片" })).toBeTruthy();
    expect(screen.getByText("略過：Shorts")).toBeTruthy();
    expect((screen.getByLabelText("檢查頻率") as HTMLSelectElement).value).toBe("6h");
    await expectNoA11yViolations(container);
  });

  it("儲存只送改過的欄位", async () => {
    const api = subsApi({
      "PATCH /api/subscriptions/{id}": () => subscription("s1", { schedule: "12h" }),
    });
    signIn();
    const user = userEvent.setup();
    renderRoute("/subscriptions/s1");
    await screen.findByRole("heading", { level: 1, name: "码农高天" });
    await user.selectOptions(screen.getByLabelText("檢查頻率"), "12h");
    await user.click(screen.getByRole("button", { name: "儲存變更" }));
    await waitFor(() => expect(api.calls("PATCH", "/api/subscriptions/s1")).toHaveLength(1));
    const body = JSON.parse(String(api.calls("PATCH", "/api/subscriptions/s1")[0]!.init.body));
    expect(body).toEqual({ schedule: "12h" });
  });

  it("停用與刪除都要先確認；刪除後回到列表", async () => {
    const api = subsApi({
      "PATCH /api/subscriptions/{id}": () =>
        subscription("s1", { enabled: false, next_check_at: null }),
      "DELETE /api/subscriptions/{id}": () => new Response(null, { status: 204 }),
    });
    signIn();
    const user = userEvent.setup();
    const { router } = renderRoute("/subscriptions/s1");
    await screen.findByRole("heading", { level: 1, name: "码农高天" });

    await user.click(screen.getByRole("button", { name: "停用" }));
    let dialog = await screen.findByRole("dialog", { name: /停用訂閱/ });
    expect(api.calls("PATCH", "/api/subscriptions/s1")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "停用" }));
    await waitFor(() => expect(api.calls("PATCH", "/api/subscriptions/s1")).toHaveLength(1));
    expect(JSON.parse(String(api.calls("PATCH", "/api/subscriptions/s1")[0]!.init.body))).toEqual({
      enabled: false,
    });

    await user.click(screen.getByRole("button", { name: "刪除訂閱" }));
    dialog = await screen.findByRole("dialog", { name: /刪除訂閱/ });
    expect(within(dialog).getByText(/已建立的影音都保留/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "刪除訂閱" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/subscriptions"));
    expect(api.calls("DELETE", "/api/subscriptions/s1")).toHaveLength(1);
  });

  it("立即檢查", async () => {
    const api = subsApi({
      "POST /api/subscriptions/{id}/check": () =>
        json(202, { next_check_at: "2026-11-23T00:00:00Z" }),
    });
    signIn();
    const user = userEvent.setup();
    renderRoute("/subscriptions/s1");
    await user.click(await screen.findByRole("button", { name: "立即檢查" }));
    await waitFor(() => expect(api.calls("POST", "/api/subscriptions/s1/check")).toHaveLength(1));
    expect(await screen.findByText(/已排定在下一輪檢查/)).toBeTruthy();
  });

  it("不存在的訂閱顯示說明", async () => {
    subsApi();
    signIn();
    renderRoute("/subscriptions/nope");
    expect(await screen.findByText(/找不到這個訂閱/)).toBeTruthy();
  });
});
