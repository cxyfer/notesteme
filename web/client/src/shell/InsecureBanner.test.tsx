import { screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { apiError, mockApi } from "../test/mockApi";
import { renderRoute, signIn } from "../test/render";

const library = { "GET /api/items": () => ({ items: [], total: 0 }) };

describe("無驗證模式（AUTH-R3）", () => {
  it("沒有 CSRF cookie 也不導到登入頁，頂部持續顯示警告", async () => {
    mockApi({
      ...library,
      "GET /api/auth/methods": () => ({ oidc: { enabled: false }, insecure_no_auth: true }),
    });
    const { router } = renderRoute("/library");
    await screen.findByRole("heading", { level: 1, name: "媒體庫" });
    expect(router.state.location.pathname).toBe("/library");
    expect((await screen.findByRole("alert")).textContent).toContain("--insecure-no-auth");
  });

  it("登入頁在無驗證模式直接回到要去的頁面", async () => {
    mockApi({
      ...library,
      "GET /api/auth/methods": () => ({ oidc: { enabled: false }, insecure_no_auth: true }),
    });
    const { router } = renderRoute("/login?redirect=%2Fqueue");
    await waitFor(() => expect(router.state.location.pathname).toBe("/queue"));
  });

  it("一般模式不顯示警告；查詢失敗時照常導到登入頁", async () => {
    mockApi({
      ...library,
      "GET /api/auth/methods": () => ({ oidc: { enabled: false }, insecure_no_auth: false }),
    });
    signIn();
    renderRoute("/library");
    await screen.findByRole("heading", { level: 1, name: "媒體庫" });
    expect(screen.queryByText(/--insecure-no-auth/)).toBeNull();
    document.cookie = "notesteme_csrf=; path=/; max-age=0";

    mockApi({ "GET /api/auth/methods": () => apiError(500, "internal_error") });
    const { router } = renderRoute("/library");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
  });
});
