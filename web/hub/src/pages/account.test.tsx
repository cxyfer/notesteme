import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import * as f from "../test/fixtures";
import { apiError, json, mockApi } from "../test/mockApi";
import { renderRoute } from "../test/render";
import { browser } from "./account/Identities";

afterEach(() => vi.restoreAllMocks());

describe("下架申請", () => {
  it("不需要登入；欄位錯誤顯示在欄位旁；成功後顯示申請編號", async () => {
    let n = 0;
    const api = mockApi({
      "GET /api/v1/me": f.meHandler(null),
      "POST /api/v1/takedowns": () =>
        n++ === 0
          ? apiError(422, "invalid_request", "invalid", {
              errors: [{ path: "statement", message: "請以 20～5000 個字元說明理由。" }],
            })
          : json(201, { id: "t9", state: "open", message: "已收到下架申請，編號 t9。" }),
    });
    renderRoute("/takedown?version=v1");
    expect(((await screen.findByLabelText("版本 ID")) as HTMLInputElement).value).toBe("v1");
    await userEvent.type(screen.getByLabelText("說明"), "short");
    await userEvent.type(screen.getByLabelText("聯絡 email"), "a@example.org");
    const submit = screen.getByRole("button", { name: "送出下架申請" }) as HTMLButtonElement;
    expect(submit.disabled).toBe(true); // 要先勾善意聲明
    await userEvent.click(screen.getByLabelText(/善意/));
    await userEvent.click(submit);
    expect(await screen.findByText("請以 20～5000 個字元說明理由。")).toBeTruthy();
    await userEvent.click(submit);
    expect(await screen.findByRole("heading", { name: "已收到下架申請" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "查詢處理狀態" }).getAttribute("href")).toBe(
      "/takedowns/t9",
    );
    expect(JSON.parse(String(api.calls("POST", "/api/v1/takedowns")[1]!.init.body))).toEqual({
      version_id: "v1",
      reason: "copyright",
      statement: "short",
      contact_email: "a@example.org",
      good_faith: true,
    });
  });

  it("查詢申請狀態", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(null),
      "GET /api/v1/takedowns/{id}": () => ({ id: "t1", state: "rejected" }),
    });
    renderRoute("/takedowns/t1");
    expect(await screen.findByText("已駁回")).toBeTruthy();
  });
});

describe("帳號頁", () => {
  it("沒有登入時導到登入頁", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(null),
      "GET /api/v1/auth/providers": () => f.providers,
    });
    const { router } = renderRoute("/account");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
  });

  it("連結登入方式導向 authorize URL；超過 10 分鐘要重新登入", async () => {
    const assign = vi.spyOn(browser, "assign").mockImplementation(() => {});
    let reauth = false;
    mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/auth/providers": () => f.providers,
      "GET /api/v1/me/tokens": () => ({ tokens: [] }),
      "POST /api/v1/me/identities/{provider}/link": () =>
        reauth
          ? apiError(403, "reauth_required", "請重新登入後再連結新的登入方式。")
          : { authorize_url: "https://accounts.google.com/o/oauth2/v2/auth?x=1" },
    });
    const { router } = renderRoute("/account");
    await userEvent.click(await screen.findByRole("button", { name: "連結 Google" }));
    await waitFor(() =>
      expect(assign).toHaveBeenCalledWith("https://accounts.google.com/o/oauth2/v2/auth?x=1"),
    );
    // 已連結的 GitHub 不出現在連結按鈕中
    expect(screen.queryByRole("button", { name: "連結 GitHub" })).toBeNull();
    reauth = true;
    await userEvent.click(screen.getByRole("button", { name: "連結 Linux.do" }));
    await userEvent.click(await screen.findByRole("button", { name: "重新登入" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
  });

  it("移除唯一的登入方式時顯示 409 的說明", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/auth/providers": () => f.providers,
      "GET /api/v1/me/tokens": () => ({ tokens: [] }),
      "DELETE /api/v1/me/identities/{provider}": () =>
        apiError(409, "last_identity", "這是你唯一的登入方式，不能移除。"),
    });
    renderRoute("/account");
    await userEvent.click(await screen.findByRole("button", { name: "移除 GitHub" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "移除登入方式" }));
    expect(await within(dialog).findByText("這是你唯一的登入方式，不能移除。")).toBeTruthy();
  });

  it("建立 Hub token：只能選帳號有的權限，token 只顯示一次；撤銷先確認", async () => {
    let tokens = [] as {
      id: string;
      name: string;
      permissions: string[];
      created_at: string;
      expires_at: string;
    }[];
    const api = mockApi({
      "GET /api/v1/me": f.meHandler(f.contributor),
      "GET /api/v1/auth/providers": () => f.providers,
      "GET /api/v1/me/shares": () => ({ shares: [] }),
      "GET /api/v1/me/tokens": () => ({ tokens }),
      "POST /api/v1/me/tokens": ({ body }) => {
        const b = body as { name: string; permissions: string[] };
        tokens = [
          {
            id: "k1",
            name: b.name,
            permissions: b.permissions,
            created_at: "2026-11-24T00:00:00Z",
            expires_at: "2027-02-22T00:00:00Z",
          },
        ];
        return json(201, { ...tokens[0], token: "nsh_" + "A".repeat(40) });
      },
      "DELETE /api/v1/me/tokens/{id}": () => new Response(null, { status: 204 }),
    });
    renderRoute("/account");
    await screen.findByText("還沒有 Hub token。");
    const perms = screen.getByRole("group", { name: "權限" });
    expect(
      within(perms)
        .getAllByRole("checkbox")
        .map((c) => c.parentElement?.textContent),
    ).toEqual(["瀏覽", "下載", "取用原始字幕", "分享"]);
    await userEvent.type(screen.getByLabelText("名稱"), "my client");
    await userEvent.click(within(perms).getByLabelText("取用原始字幕"));
    await userEvent.click(screen.getByRole("button", { name: "建立 token" }));
    const dialog = await screen.findByRole("dialog", { name: "已建立 Hub token" });
    expect(within(dialog).getByText("nsh_" + "A".repeat(40))).toBeTruthy();
    expect(JSON.parse(String(api.calls("POST", "/api/v1/me/tokens")[0]!.init.body))).toEqual({
      name: "my client",
      permissions: ["read", "download", "regenerate"],
      expires_in_days: 90,
    });
    await userEvent.click(within(dialog).getByRole("button", { name: "關閉" }));
    await userEvent.click(await screen.findByRole("button", { name: "撤銷 my client" }));
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "撤銷 token" }),
    );
    await waitFor(() => expect(api.calls("DELETE", "/api/v1/me/tokens/k1")).toHaveLength(1));
    expect(screen.queryByText("nsh_" + "A".repeat(40))).toBeNull();
  });

  it("我的分享：顯示移除原因，可以撤回", async () => {
    const api = mockApi({
      "GET /api/v1/me": f.meHandler(f.contributor),
      "GET /api/v1/auth/providers": () => f.providers,
      "GET /api/v1/me/tokens": () => ({ tokens: [] }),
      "GET /api/v1/me/shares": () => ({ shares: f.shares }),
      "DELETE /api/v1/shares/{id}": () => new Response(null, { status: 204 }),
    });
    renderRoute("/account");
    expect(await screen.findByText(/依下架申請移除：copyright/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "撤回分享" }));
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "撤回分享" }),
    );
    await waitFor(() => expect(api.calls("DELETE", "/api/v1/shares/s1")).toHaveLength(1));
  });

  it("沒有 upload 權限時不顯示我的分享", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/auth/providers": () => f.providers,
      "GET /api/v1/me/tokens": () => ({ tokens: [] }),
    });
    renderRoute("/account");
    await screen.findByRole("heading", { name: "Hub token" });
    expect(screen.queryByRole("heading", { name: "我的分享" })).toBeNull();
  });
});
