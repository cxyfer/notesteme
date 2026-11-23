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

// 後端的登入：回應中的 next 是請求的 next 通過檢查後的值（ENTRY-R6）；這裡只模擬「以 / 開頭、第二個字元不是 /」。
async function echoNext(_input: unknown, init?: RequestInit) {
  document.cookie = "notesteme_csrf=t; path=/";
  const body = JSON.parse(String(init?.body ?? "{}")) as { next?: string };
  const n = body.next ?? "/";
  const next = n.startsWith("/") && !n.startsWith("//") ? n : "/";
  return json(200, { expires_at: "2026-12-01T00:00:00Z", next });
}

// 登入頁也會讀 GET /api/auth/methods；取出登入的那一次請求。
function loginCall() {
  const call = fetchMock.mock.calls.find(([u]) => u === "/api/auth/login");
  if (!call) throw new Error("沒有送出 POST /api/auth/login");
  return call;
}

async function submit(password: string) {
  const input = await screen.findByLabelText("密碼");
  await userEvent.clear(input);
  await userEvent.type(input, password);
  await userEvent.click(screen.getByRole("button", { name: "登入" }));
}

// /add 之後的媒體庫與「新增」抽屜會讀的 API
function mockAddRoutes() {
  const login = fetchMock.getMockImplementation()!;
  fetchMock.mockImplementation(async (input, init) => {
    if (input === "/api/auth/login") return login(input, init);
    if (input === "/api/resolve") return json(422, { code: "unrecognized_url", message: "x" });
    if (String(input).startsWith("/api/profiles")) return json(200, { profiles: [] });
    return json(200, { items: [], total: 0 });
  });
}

describe("登入頁", () => {
  it("密碼錯誤顯示「密碼不正確。」", async () => {
    fetchMock.mockImplementation(async () =>
      json(401, { code: "invalid_password", message: "invalid password" }),
    );
    renderRoute("/login");
    await submit("wrong");
    expect((await screen.findByRole("alert")).textContent).toContain("密碼不正確。");
    const [url, init] = loginCall();
    expect(url).toBe("/api/auth/login");
    expect(init!.body).toBe('{"password":"wrong"}');
  });

  it("鎖定時顯示剩餘秒數並停用按鈕，時間到後恢復", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      fetchMock.mockImplementation(async () =>
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
    fetchMock.mockImplementation(echoNext);
    const { router } = renderRoute("/login?redirect=%2Fqueue");
    await submit("pw");
    await screen.findByRole("heading", { level: 1, name: "佇列" });
    expect(router.state.location.pathname).toBe("/queue");
  });

  it("導回帶 query 的路徑", async () => {
    fetchMock.mockImplementation(echoNext);
    const { router } = renderRoute("/login?redirect=%2Fqueue%3Fx%3D1");
    await submit("pw");
    await screen.findByRole("heading", { level: 1, name: "佇列" });
    expect(router.state.location.pathname).toBe("/queue");
    expect(router.state.location.searchStr).toBe("?x=1");
  });

  it("不安全的 redirect（//evil.example）不送出，回到首頁（UI-7 起是總覽）", async () => {
    fetchMock.mockImplementation(echoNext);
    const { router } = renderRoute("/login?redirect=%2F%2Fevil.example");
    await submit("pw");
    await screen.findByRole("heading", { level: 1, name: "總覽" });
    expect(router.state.location.pathname).toBe("/");
  });

  it("登入後前往回應中的 next（分享帶來的網址，ENTRY-R5、R6）", async () => {
    fetchMock.mockImplementation(echoNext);
    mockAddRoutes();
    const { router } = renderRoute(
      "/login?next=%2Fadd%3Furl%3Dhttps%253A%252F%252Fyoutu.be%252Fabc123xxxxx",
    );
    await submit("pw");
    const [, init] = loginCall();
    expect(JSON.parse(String(init!.body))).toEqual({
      password: "pw",
      next: "/add?url=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx",
    });
    // /add 打開「新增」抽屜並帶入網址，再換成媒體庫
    expect(await screen.findByDisplayValue("https://youtu.be/abc123xxxxx")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/library");
  });

  it("不使用登入頁網址中的 next，只用回應中的 next", async () => {
    fetchMock.mockImplementation(async () => {
      document.cookie = "notesteme_csrf=t; path=/";
      return json(200, { expires_at: "2026-12-01T00:00:00Z", next: "/" });
    });
    const { router } = renderRoute("/login?next=%2Fqueue");
    await submit("pw");
    await screen.findByRole("heading", { level: 1, name: "總覽" });
    expect(router.state.location.pathname).toBe("/");
  });

  it("登出後回到登入頁", async () => {
    document.cookie = "notesteme_csrf=t; path=/";
    fetchMock.mockImplementation(async (input) => {
      if (input !== "/api/auth/logout") {
        return new Response(JSON.stringify({ items: [], total: 0 }), { status: 200 });
      }
      document.cookie = "notesteme_csrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/";
      return new Response(null, { status: 204 });
    });
    const { router } = renderRoute("/library");
    await userEvent.click(await screen.findByRole("button", { name: "登出" }));
    await screen.findByLabelText("密碼");
    expect(router.state.location.pathname).toBe("/login");
    // 媒體庫一打開就會讀列表：找出登出的那一次呼叫
    const call = fetchMock.mock.calls.find(([u]) => u === "/api/auth/logout");
    expect(call).toBeDefined();
    const [, init] = call!;
    expect((init!.headers as Record<string, string>)["X-CSRF-Token"]).toBe("t");
  });

  it("OIDC 啟用時顯示「用 <主機名稱> 登入」，連到 start 並帶上要回去的頁面", async () => {
    fetchMock.mockImplementation(async (input) =>
      input === "/api/auth/methods"
        ? json(200, { oidc: { enabled: true, host: "id.example.com" } })
        : json(401, { code: "invalid_password", message: "x" }),
    );
    renderRoute("/login?next=%2Fitems%2Fabc%3Ft%3D12");
    const link = await screen.findByRole("link", { name: "用 id.example.com 登入" });
    expect(link.getAttribute("href")).toBe("/api/auth/oidc/start?next=%2Fitems%2Fabc%3Ft%3D12");
  });

  it("沒有啟用 OIDC 或讀取失敗時不顯示 OIDC 按鈕", async () => {
    fetchMock.mockImplementation(async (input) =>
      input === "/api/auth/methods" ? json(200, { oidc: { enabled: false } }) : json(500, {}),
    );
    renderRoute("/login");
    await screen.findByLabelText("密碼");
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(screen.queryByRole("link")).toBeNull();
  });
});
