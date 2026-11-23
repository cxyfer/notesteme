import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { notifier } from "../../test/m4Fixtures";
import { apiError, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { digest } from "../../test/ui5Fixtures";

function digestApi(extra: Record<string, MockHandler> = {}, digests = [digest("morning")]) {
  return mockApi({
    "GET /api/notifiers": () => ({
      notifiers: [
        notifier("ops", { events: ["digest", "item.failed"] }),
        notifier("phone", { events: ["item.done"] }),
      ],
    }),
    "GET /api/notifications": () => ({ notifications: [] }),
    "GET /api/digests": () => ({ digests }),
    ...extra,
  });
}

async function open() {
  signIn();
  const r = renderRoute("/settings/notifications");
  await screen.findByRole("heading", { level: 3, name: "彙整" });
  return r;
}

describe("設定：彙整（DIG-R8～R11）", () => {
  it("列出彙整；預覽顯示內容但不送出（axe 無違規）", async () => {
    const api = digestApi({
      "POST /api/digests/{id}/preview": () => ({
        items: [
          { id: "i1", title: "影片", title_translation: null, summary: "s", awaiting_user: true },
        ],
        text: "Notesteme 每日彙整：2026-11-24（1 部）",
      }),
    });
    const { container } = await open();
    const row = (await screen.findByText(/^morning · 每天 08:00/)).closest("li")!;
    expect(within(row).getByText(/送到 ops/)).toBeTruthy();
    await expectNoA11yViolations(container);
    await userEvent.setup().click(within(row).getByRole("button", { name: "預覽" }));
    expect(await within(row).findByText(/每日彙整：2026-11-24/)).toBeTruthy();
    expect(api.calls("POST", "/api/digests/morning/preview")).toHaveLength(1);
  });

  it("新增：只能選勾了彙整事件的端點；每週要選星期；422 逐欄顯示", async () => {
    let body: unknown;
    digestApi(
      {
        "POST /api/digests": (req) => {
          body = req.body;
          return apiError(422, "invalid_request", "請求不正確", {
            errors: [{ path: "timezone", message: "不認得的時區。" }],
          });
        },
      },
      [],
    );
    await open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "新增彙整" }));
    const dialog = await screen.findByRole("dialog", { name: "新增彙整" });
    expect(within(dialog).queryByLabelText("phone")).toBeNull();
    expect((within(dialog).getByLabelText("ops") as HTMLInputElement).checked).toBe(true);
    await user.type(within(dialog).getByLabelText("id"), "weekly-sum");
    await user.selectOptions(within(dialog).getByLabelText("頻率"), "weekly");
    await user.selectOptions(within(dialog).getByLabelText("星期"), "fri");
    const tz = within(dialog).getByLabelText("時區");
    await user.clear(tz);
    await user.type(tz, "Mars/Base");
    await user.click(within(dialog).getByRole("button", { name: "儲存變更" }));
    expect(await within(dialog).findByText("不認得的時區。")).toBeTruthy();
    expect(body).toEqual({
      id: "weekly-sum",
      period: "weekly",
      weekday: "fri",
      at: "08:00",
      timezone: "Mars/Base",
      scope: "subscriptions",
      notifiers: ["ops"],
    });
  });

  it("編輯只送改過的欄位；停用先確認", async () => {
    const patches: unknown[] = [];
    digestApi({
      "PATCH /api/digests/{id}": (req) => {
        patches.push(req.body);
        return digest("morning");
      },
    });
    await open();
    const user = userEvent.setup();
    const row = (await screen.findByText(/^morning · /)).closest("li")!;
    await user.click(within(row).getByRole("button", { name: "編輯" }));
    const dialog = await screen.findByRole("dialog", { name: "編輯彙整 morning" });
    await user.selectOptions(within(dialog).getByLabelText("包含哪些影音"), "all");
    await user.click(within(dialog).getByRole("button", { name: "儲存變更" }));
    await waitFor(() => expect(patches).toEqual([{ scope: "all" }]));
    await user.click(within(row).getByRole("button", { name: "停用" }));
    const confirm = await screen.findByRole("dialog", { name: "停用彙整 morning？" });
    await user.click(within(confirm).getByRole("button", { name: "停用" }));
    await waitFor(() => expect(patches.at(-1)).toEqual({ enabled: false }));
  });

  it("沒有端點勾彙整事件時說明，不能新增", async () => {
    mockApi({
      "GET /api/notifiers": () => ({ notifiers: [notifier("phone", { events: ["item.done"] })] }),
      "GET /api/notifications": () => ({ notifications: [] }),
      "GET /api/digests": () => ({ digests: [] }),
    });
    await open();
    expect(await screen.findByText(/先在上面的通知端點勾選「彙整」事件/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "新增彙整" }).hasAttribute("disabled")).toBe(true);
  });
});
