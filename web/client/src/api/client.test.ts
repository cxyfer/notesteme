import i18next from "i18next";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, apiPath, request, setUnauthorizedHandler } from "./client";
import { ApiError } from "./errors";

const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
  document.cookie = "notesteme_csrf=tok%3D1; path=/";
});
afterEach(() => {
  vi.unstubAllGlobals();
  setUnauthorizedHandler(undefined);
});

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

function lastInit() {
  const call = fetchMock.mock.calls.at(-1)!;
  return {
    url: call[0] as string,
    init: call[1]!,
    headers: call[1]!.headers as Record<string, string>,
  };
}

describe("request", () => {
  it("GET 不帶 CSRF；帶 cookie 與 Accept-Language", async () => {
    fetchMock.mockResolvedValue(json(200, { ok: 1 }));
    await expect(api.get("/api/queue")).resolves.toEqual({ ok: 1 });
    const { init, headers } = lastInit();
    expect(init.credentials).toBe("same-origin");
    expect(headers["X-CSRF-Token"]).toBeUndefined();
    expect(headers["Accept-Language"]).toBe("zh-Hant");
  });

  it("非 GET 加上 X-CSRF-Token（解碼後的 cookie 值）與 JSON 本體", async () => {
    fetchMock.mockResolvedValue(json(200, { expires_at: "2026-11-23T00:00:00Z" }));
    await api.post("/api/auth/login", { body: { password: "pw" } });
    const { init, headers } = lastInit();
    expect(init.method).toBe("POST");
    expect(headers["X-CSRF-Token"]).toBe("tok=1");
    expect(headers["Content-Type"]).toBe("application/json");
    expect(init.body).toBe('{"password":"pw"}');
  });

  it("Accept-Language 跟隨 UI 語言", async () => {
    await i18next.changeLanguage("en");
    fetchMock.mockResolvedValue(json(200, {}));
    await api.get("/api/queue");
    expect(lastInit().headers["Accept-Language"]).toBe("en");
  });

  it("錯誤轉成帶 code 與 message 的 ApiError", async () => {
    fetchMock.mockResolvedValue(
      json(429, { code: "login_locked", message: "已鎖定", retry_after_s: 42 }),
    );
    const err = await api
      .post("/api/auth/login", { body: { password: "x" } })
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 429, code: "login_locked", message: "已鎖定" });
    expect((err as ApiError).body.retry_after_s).toBe(42);
  });

  it("401 unauthorized 通知 handler；invalid_password 的 401 不通知", async () => {
    const onUnauth = vi.fn();
    setUnauthorizedHandler(onUnauth);
    fetchMock.mockResolvedValueOnce(json(401, { code: "unauthorized", message: "" }));
    await expect(api.get("/api/queue")).rejects.toMatchObject({ code: "unauthorized" });
    expect(onUnauth).toHaveBeenCalledOnce();
    fetchMock.mockResolvedValueOnce(json(401, { code: "invalid_password", message: "" }));
    await expect(api.post("/api/auth/login", { body: { password: "x" } })).rejects.toMatchObject({
      code: "invalid_password",
    });
    expect(onUnauth).toHaveBeenCalledOnce();
  });

  it("網路錯誤是 network_error；非 JSON 的錯誤本體是 bad_response", async () => {
    fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await expect(api.get("/api/queue")).rejects.toMatchObject({ status: 0, code: "network_error" });
    fetchMock.mockResolvedValueOnce(
      new Response("<html>", { status: 502, statusText: "Bad Gateway" }),
    );
    await expect(api.get("/api/queue")).rejects.toMatchObject({
      status: 502,
      code: "bad_response",
    });
  });

  it("204 回傳 undefined；query 參數略過 undefined", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await expect(
      request("DELETE", "/api/x", { query: { a: 1, b: undefined, c: "x y" } }),
    ).resolves.toBeUndefined();
    expect(lastInit().url).toBe("/api/x?a=1&c=x+y");
  });
});

describe("apiPath", () => {
  it("代入並編碼路徑參數", () => {
    expect(apiPath("/api/items/{id}/cues", { id: "yt:a/b" })).toBe("/api/items/yt%3Aa%2Fb/cues");
    expect(() => apiPath("/api/items/{id}")).toThrow();
  });
});
