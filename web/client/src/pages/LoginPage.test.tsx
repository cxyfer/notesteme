import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { renderRoute } from "../test/render";

const fetchMock = vi.fn<typeof fetch>();
const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

async function submit(password: string) {
  const input = await screen.findByLabelText("密碼");
  await userEvent.clear(input);
  await userEvent.type(input, password);
  await userEvent.click(screen.getByRole("button", { name: "登入" }));
}

describe("登入頁", () => {
  it("密碼錯誤顯示「密碼不正確。」", async () => {
    fetchMock.mockResolvedValue(
      json(401, { code: "invalid_password", message: "invalid password" }),
    );
    renderRoute("/login");
    await submit("wrong");
    expect((await screen.findByRole("alert")).textContent).toContain("密碼不正確。");
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("/api/auth/login");
    expect(init!.body).toBe('{"password":"wrong"}');
  });

  it("鎖定時顯示剩餘秒數並停用按鈕，時間到後恢復", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      fetchMock.mockResolvedValue(
        json(429, { code: "login_locked", message: "locked", retry_after_s: 3 }),
      );
      renderRoute("/login");
      await submit("x");
      const alert = await screen.findByRole("alert");
      expect(alert.textContent).toContain("3 秒後再試");
      expect(alert.textContent).toContain("稍後會自動重試");
      expect(screen.getByRole("button", { name: "登入" })).toHaveProperty("disabled", true);
      await vi.advanceTimersByTimeAsync(3100);
      await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
      expect(screen.getByRole("button", { name: "登入" })).toHaveProperty("disabled", false);
    } finally {
      vi.useRealTimers();
    }
  });

  it("登入後導回原本的路徑", async () => {
    fetchMock.mockImplementation(async () => {
      document.cookie = "notesteme_csrf=t; path=/";
      return json(200, { expires_at: "2026-12-01T00:00:00Z" });
    });
    const { router } = renderRoute("/login?redirect=%2Fqueue");
    await submit("pw");
    await screen.findByRole("heading", { level: 1, name: "佇列" });
    expect(router.state.location.pathname).toBe("/queue");
  });

  it("導回帶 query 的路徑", async () => {
    fetchMock.mockImplementation(async () => {
      document.cookie = "notesteme_csrf=t; path=/";
      return json(200, { expires_at: "2026-12-01T00:00:00Z" });
    });
    const { router } = renderRoute("/login?redirect=%2Fqueue%3Fx%3D1");
    await submit("pw");
    await screen.findByRole("heading", { level: 1, name: "佇列" });
    expect(router.state.location.pathname).toBe("/queue");
    expect(router.state.location.searchStr).toBe("?x=1");
  });

  it("不安全的 redirect（//evil.example）改成 /library", async () => {
    fetchMock.mockImplementation(async () => {
      document.cookie = "notesteme_csrf=t; path=/";
      return json(200, { expires_at: "2026-12-01T00:00:00Z" });
    });
    const { router } = renderRoute("/login?redirect=%2F%2Fevil.example");
    await submit("pw");
    await screen.findByRole("heading", { level: 1, name: "媒體庫" });
    expect(router.state.location.pathname).toBe("/library");
  });

  it("登出後回到登入頁", async () => {
    document.cookie = "notesteme_csrf=t; path=/";
    fetchMock.mockImplementation(async () => {
      document.cookie = "notesteme_csrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/";
      return new Response(null, { status: 204 });
    });
    const { router } = renderRoute("/library");
    await userEvent.click(await screen.findByRole("button", { name: "登出" }));
    await screen.findByLabelText("密碼");
    expect(router.state.location.pathname).toBe("/login");
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("/api/auth/logout");
    expect((init!.headers as Record<string, string>)["X-CSRF-Token"]).toBe("t");
  });
});
