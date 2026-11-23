import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { notifier } from "../../test/m4Fixtures";
import { apiError, json, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";

const SECRET = "https://hooks.example.com/SECRET-path-abcd";

function notifyApi(extra: Record<string, MockHandler> = {}) {
  return mockApi({
    "GET /api/notifiers": () => ({
      notifiers: [
        notifier("ops"),
        notifier("phone", {
          kind: "ntfy",
          config: { server: "https://ntfy.sh", topic: "notesteme", priority: 3 },
          events: ["item.done"],
          enabled: false,
        }),
      ],
    }),
    "GET /api/notifications": () => ({
      notifications: [
        {
          id: "o1",
          notifier: "ops",
          event: "item.failed",
          state: "dropped",
          attempts: 6,
          last_error: "HTTP 500",
          created_at: "2026-11-22T00:00:00Z",
          sent_at: null,
        },
      ],
    }),
    "GET /api/settings/budget": () => ({
      monthly_usd: 20,
      per_item_usd: null,
      warn_at_percent: 80,
      timezone: "Asia/Taipei",
      month: "2026-11",
      spent_usd: 3.42,
      reserved_usd: 0.1,
      remaining_usd: 16.48,
    }),
    ...extra,
  });
}

async function open(section: string) {
  signIn();
  const r = renderRoute(`/settings/${section}`);
  await screen.findByRole("heading", { level: 1, name: "設定" });
  return r;
}

describe("設定：通知", () => {
  it("列出端點，密鑰只顯示主機與末四碼；最近的通知（axe 無違規）", async () => {
    notifyApi();
    const { container } = await open("notifications");
    expect(await screen.findByText(/hooks\.example\.com，末四碼 abcd/)).toBeTruthy();
    expect(screen.getByText(/phone · ntfy · 已停用/)).toBeTruthy();
    expect(await screen.findByText(/已放棄 · 嘗試 6 次 · HTTP 500/)).toBeTruthy();
    await expectNoA11yViolations(container);
  });

  it("新增 webhook：密鑰送出後就從畫面清掉；422 逐欄顯示", async () => {
    let n = 0;
    const api = notifyApi({
      "POST /api/notifiers": () =>
        ++n === 1
          ? apiError(422, "invalid_request", "invalid", {
              errors: [{ path: "config.secret", message: "secret 必須是 16 到 256 個字元。" }],
            })
          : json(201, notifier("hook")),
    });
    const user = userEvent.setup();
    await open("notifications");
    await user.click(await screen.findByRole("button", { name: "新增通知端點" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("id"), "hook");
    await user.selectOptions(within(dialog).getByLabelText("種類"), "webhook");
    await user.type(within(dialog).getByLabelText("網址"), SECRET);
    await user.type(within(dialog).getByLabelText("簽章用的 secret"), "short");
    await user.click(within(dialog).getByRole("button", { name: "儲存變更" }));
    expect(await within(dialog).findByText(/secret 必須是 16 到 256 個字元/)).toBeTruthy();
    // 失敗也要重新輸入：明文不留在畫面上
    expect(document.body.innerHTML).not.toContain("SECRET-path");
    const body = JSON.parse(String(api.calls("POST", "/api/notifiers")[0]!.init.body));
    expect(body).toEqual({
      id: "hook",
      kind: "webhook",
      config: { url: SECRET, secret: "short" },
      events: ["item.failed", "item.blocked"],
    });
  });

  it("編輯時沒有重新輸入的密鑰不送；只送改過的事件", async () => {
    const api = notifyApi({ "PATCH /api/notifiers/{id}": () => notifier("ops") });
    const user = userEvent.setup();
    await open("notifications");
    const row = (await screen.findByText(/^ops · webhook/)).closest("li")!;
    await user.click(within(row).getByRole("button", { name: "編輯" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/已設定（末四碼 abcd）/)).toBeTruthy();
    await user.click(within(dialog).getByLabelText(/處理完成/));
    await user.click(within(dialog).getByRole("button", { name: "儲存變更" }));
    await waitFor(() => expect(api.calls("PATCH", "/api/notifiers/ops")).toHaveLength(1));
    expect(JSON.parse(String(api.calls("PATCH", "/api/notifiers/ops")[0]!.init.body))).toEqual({
      events: ["item.done", "item.failed", "item.blocked"],
    });
  });

  it("停用與刪除先確認；測試通知顯示結果", async () => {
    const api = notifyApi({
      "PATCH /api/notifiers/{id}": () => notifier("ops", { enabled: false }),
      "DELETE /api/notifiers/{id}": () => new Response(null, { status: 204 }),
      "POST /api/notifiers/{id}/test": () => ({
        ok: false,
        error_class: "permanent",
        message: "通知端點回應 HTTP 404。",
      }),
    });
    const user = userEvent.setup();
    await open("notifications");
    const row = (await screen.findByText(/^ops · webhook/)).closest("li")!;
    await user.click(within(row).getByRole("button", { name: "傳送測試通知" }));
    expect(await within(row).findByText(/測試通知沒有送達：通知端點回應 HTTP 404/)).toBeTruthy();

    await user.click(within(row).getByRole("button", { name: "停用" }));
    let dialog = await screen.findByRole("dialog", { name: /停用通知端點/ });
    expect(api.calls("PATCH", "/api/notifiers/ops")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "停用" }));
    await waitFor(() => expect(api.calls("PATCH", "/api/notifiers/ops")).toHaveLength(1));

    await user.click(within(row).getByRole("button", { name: "刪除" }));
    dialog = await screen.findByRole("dialog", { name: /刪除通知端點/ });
    await user.click(within(dialog).getByRole("button", { name: "刪除" }));
    await waitFor(() => expect(api.calls("DELETE", "/api/notifiers/ops")).toHaveLength(1));
  });
});

describe("設定：預算", () => {
  it("只送四個欄位，空白送 null（axe 無違規）", async () => {
    const api = notifyApi({
      "PUT /api/settings/budget": (req) => ({ ...(req.body as object), month: "2026-11" }),
    });
    const user = userEvent.setup();
    const { container } = await open("budget");
    expect(await screen.findByText(/本月已花費 US\$3\.42/)).toBeTruthy();
    await expectNoA11yViolations(container);
    const monthly = screen.getByLabelText("月預算（美元）");
    await user.clear(monthly);
    await user.click(screen.getByRole("button", { name: "儲存變更" }));
    await waitFor(() => expect(api.calls("PUT", "/api/settings/budget")).toHaveLength(1));
    expect(JSON.parse(String(api.calls("PUT", "/api/settings/budget")[0]!.init.body))).toEqual({
      monthly_usd: null,
      per_item_usd: null,
      warn_at_percent: 80,
      timezone: "Asia/Taipei",
    });
  });

  it("提醒門檻不是 1～100 的整數時不能儲存", async () => {
    notifyApi();
    const user = userEvent.setup();
    await open("budget");
    const warn = await screen.findByLabelText("提醒門檻（%）");
    await user.clear(warn);
    await user.type(warn, "150");
    expect(screen.getByText("提醒門檻必須是 1 到 100 的整數。")).toBeTruthy();
    expect((screen.getByRole("button", { name: "儲存變更" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
  });
});
