import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import * as f from "../test/fixtures";
import { apiError, mockApi } from "../test/mockApi";
import { renderRoute } from "../test/render";

describe("管理頁", () => {
  it("沒有權限時只顯示說明；導覽沒有管理入口", async () => {
    mockApi({ "GET /api/v1/me": f.meHandler(f.member) });
    renderRoute("/admin/users");
    expect(await screen.findByText("這一頁需要「管理」權限。")).toBeTruthy();
    expect(screen.queryByRole("link", { name: "審核" })).toBeNull();
  });

  it("檢舉：依狀態篩選、說明當作文字、移除前先確認並送出說明", async () => {
    const api = mockApi({
      "GET /api/v1/me": f.meHandler(f.moderator),
      "GET /api/v1/admin/reports": () => ({ reports: [f.report] }),
      "POST /api/v1/admin/reports/{id}/decision": () => ({
        id: "r1",
        state: "actioned",
        version_state: "removed",
      }),
    });
    renderRoute("/admin/reports");
    expect(await screen.findByText("<script>alert(1)</script>")).toBeTruthy();
    expect(screen.getByText("檢舉者：bob")).toBeTruthy();
    // 版主看得到審核，看不到使用者管理
    expect(screen.queryByRole("link", { name: "使用者" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "移除版本" }));
    const dialog = await screen.findByRole("dialog", { name: "移除這個版本？" });
    await userEvent.type(within(dialog).getByLabelText("說明（選填）"), "spam");
    await userEvent.click(within(dialog).getByRole("button", { name: "移除版本" }));
    await waitFor(() =>
      expect(
        JSON.parse(String(api.calls("POST", "/api/v1/admin/reports/r1/decision")[0]!.init.body)),
      ).toEqual({
        decision: "remove",
        note: "spam",
      }),
    );
    await userEvent.selectOptions(screen.getByLabelText("狀態"), "dismissed");
    await waitFor(() =>
      expect(api.calls("GET", "/api/v1/admin/reports").at(-1)!.url.searchParams.get("state")).toBe(
        "dismissed",
      ),
    );
  });

  it("下架申請：看得到聯絡 email；已決定時顯示 409", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(f.moderator),
      "GET /api/v1/admin/takedowns": () => ({
        takedowns: [f.takedown, { ...f.takedown, id: "t2", contact_email: null }],
      }),
      "POST /api/v1/admin/takedowns/{id}/decision": () =>
        apiError(409, "takedown_decided", "這個下架申請已經處理過了。"),
    });
    renderRoute("/admin/takedowns");
    expect(await screen.findByText("聯絡 email：owner@example.org")).toBeTruthy();
    expect(screen.getByText("聯絡 email：（無法解密）")).toBeTruthy();
    await userEvent.click(screen.getAllByRole("button", { name: "駁回申請" })[0]!);
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "駁回申請" }));
    expect(await within(dialog).findByText("這個下架申請已經處理過了。")).toBeTruthy();
  });

  it("使用者：指派群組、停用要寫原因、不能停用自己、admins 與 members 沒有移除按鈕", async () => {
    const api = mockApi({
      "GET /api/v1/me": f.meHandler({ ...f.admin, id: "u1" }),
      "GET /api/v1/admin/users": () => ({ users: f.users }),
      "PUT /api/v1/admin/users/{id}/groups/{group}": () => new Response(null, { status: 204 }),
      "POST /api/v1/admin/users/{id}/unsuspend": () => ({ id: "u2", suspended: false }),
      "DELETE /api/v1/admin/users/{id}/groups/{group}": () => new Response(null, { status: 204 }),
    });
    renderRoute("/admin/users");
    expect(await screen.findByText(/停用：spam/)).toBeTruthy();
    // 自己（u1）沒有停用按鈕；bob 已停用，只有恢復
    expect(screen.queryByRole("button", { name: "停用帳號" })).toBeNull();
    expect(screen.queryByRole("button", { name: /移出 members/ })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "把 alice 移出 contributors" }));
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "移出群組" }),
    );
    await waitFor(() =>
      expect(api.calls("DELETE", "/api/v1/admin/users/u1/groups/contributors")).toHaveLength(1),
    );
    await userEvent.click(screen.getAllByRole("button", { name: "加入群組" })[1]!);
    const dialog = await screen.findByRole("dialog");
    const input = within(dialog).getByLabelText("群組名稱");
    await userEvent.clear(input);
    await userEvent.type(input, "moderators");
    await userEvent.click(within(dialog).getByRole("button", { name: "加入群組" }));
    await waitFor(() =>
      expect(api.calls("PUT", "/api/v1/admin/users/u2/groups/moderators")).toHaveLength(1),
    );
    await userEvent.click(screen.getByRole("button", { name: "恢復帳號" }));
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "恢復帳號" }),
    );
    await waitFor(() =>
      expect(api.calls("POST", "/api/v1/admin/users/u2/unsuspend")).toHaveLength(1),
    );
  });

  it("停用帳號送出原因", async () => {
    const api = mockApi({
      "GET /api/v1/me": f.meHandler(f.admin),
      "GET /api/v1/admin/users": () => ({ users: [f.users[0]] }),
      "POST /api/v1/admin/users/{id}/suspend": () => ({ id: "u1", suspended: true }),
    });
    renderRoute("/admin/users");
    await userEvent.click(await screen.findByRole("button", { name: "停用帳號" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText("說明（選填）"), "abuse");
    await userEvent.click(within(dialog).getByRole("button", { name: "停用帳號" }));
    await waitFor(() =>
      expect(
        JSON.parse(String(api.calls("POST", "/api/v1/admin/users/u1/suspend")[0]!.init.body)),
      ).toEqual({
        reason: "abuse",
      }),
    );
  });
});
