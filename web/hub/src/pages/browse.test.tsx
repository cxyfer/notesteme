import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import * as f from "../test/fixtures";
import { apiError, mockApi } from "../test/mockApi";
import { renderRoute } from "../test/render";

describe("登入頁", () => {
  it("依序列出已啟用的登入方式，連到 /auth/<provider>/start，並記住登入前的頁面", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(null),
      "GET /api/v1/auth/providers": () => f.providers,
    });
    renderRoute("/login?redirect=%2Fversions%2Fv1");
    const links = await screen.findAllByRole("link", { name: /^用 .+ 登入$/ });
    expect(links.map((a) => a.getAttribute("href"))).toEqual([
      "/auth/google/start",
      "/auth/github/start",
      "/auth/linuxdo/start",
    ]);
    links[1]!.addEventListener("click", (e) => e.preventDefault());
    await userEvent.click(links[1]!);
    expect(sessionStorage.getItem("notesteme-hub.returnTo")).toBe("/versions/v1");
  });

  it("不接受站外的 redirect", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(null),
      "GET /api/v1/auth/providers": () => f.providers,
    });
    renderRoute("/login?redirect=%2F%2Fevil.example");
    const link = await screen.findByRole("link", { name: "用 GitHub 登入" });
    link.addEventListener("click", (e) => e.preventDefault());
    await userEvent.click(link);
    expect(sessionStorage.getItem("notesteme-hub.returnTo")).toBe("/");
  });

  it("登入後回到登入前的頁面", async () => {
    sessionStorage.setItem("notesteme-hub.returnTo", "/takedown");
    mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/versions": () => ({ versions: [] }),
    });
    const { router } = renderRoute("/");
    await waitFor(() => expect(router.state.location.pathname).toBe("/takedown"));
  });
});

describe("瀏覽", () => {
  it("顯示分數、授權、上傳者與「未經記錄」的模型標示", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/versions": () => ({ versions: [f.item, f.unverified] }),
    });
    renderRoute("/");
    const rows = await screen.findAllByRole("article");
    expect(within(rows[0]!).getByText("由 Client 記錄的模型")).toBeTruthy();
    expect(within(rows[0]!).getByText("分數 7")).toBeTruthy();
    expect(within(rows[0]!).getByText("授權 CC-BY-4.0")).toBeTruthy();
    expect(within(rows[1]!).getByText("模型未經記錄")).toBeTruthy();
    expect(within(rows[1]!).getByText("上傳者：bob")).toBeTruthy();
    // 「由 Client 記錄」不寫成「已驗證」（ADR-0031 第 3 點）
    expect(screen.queryByText(/已驗證/)).toBeNull();
  });

  it("篩選條件寫進 URL 並原樣送到 API；422 的欄位錯誤顯示在欄位旁", async () => {
    const api = mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/versions": ({ url }) =>
        url.searchParams.get("lang") === "bad!"
          ? apiError(422, "invalid_request", "invalid", {
              errors: [{ path: "query.lang", message: "語言代碼不正確。" }],
            })
          : { versions: [f.item], next_cursor: "c2" },
    });
    const { router } = renderRoute("/");
    await screen.findAllByRole("article");
    await userEvent.type(screen.getByLabelText("校正模型"), "anthropic/claude-sonnet");
    await userEvent.click(screen.getByLabelText("排除模型未經 Client 記錄的版本"));
    await userEvent.click(screen.getByRole("button", { name: "套用篩選" }));
    await waitFor(() =>
      expect(router.state.location.search).toEqual({
        correct_model: "anthropic/claude-sonnet",
        exclude_unverified: "true",
      }),
    );
    await waitFor(() => {
      const last = api.calls("GET", "/api/v1/versions").at(-1)!.url.searchParams;
      expect(last.get("correct_model")).toBe("anthropic/claude-sonnet");
      expect(last.get("exclude_unverified")).toBe("true");
      expect(last.get("limit")).toBe("50");
    });
    // 載入更多帶 cursor
    await userEvent.click(await screen.findByRole("button", { name: "載入更多" }));
    await waitFor(() =>
      expect(api.calls("GET", "/api/v1/versions").at(-1)!.url.searchParams.get("cursor")).toBe(
        "c2",
      ),
    );

    await userEvent.type(screen.getByLabelText("語言"), "bad!");
    await userEvent.click(screen.getByRole("button", { name: "套用篩選" }));
    expect(await screen.findByText("語言代碼不正確。")).toBeTruthy();
  });

  it("沒有結果時有清除篩選條件的按鈕", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/versions": () => ({ versions: [] }),
    });
    const { router } = renderRoute("/?lang=fr");
    expect(await screen.findByText("沒有符合條件的版本。")).toBeTruthy();
    await userEvent.click(screen.getAllByRole("button", { name: "清除篩選條件" })[0]!);
    await waitFor(() => expect(router.state.location.search).toEqual({}));
  });

  it("站台需要登入時，未登入的瀏覽導到登入頁", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(null),
      "GET /api/v1/auth/providers": () => f.providers,
      "GET /api/v1/versions": () => apiError(401, "unauthorized", "請先登入 Hub。"),
    });
    const { router } = renderRoute("/?lang=en");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toEqual({ redirect: "/?lang=en" });
  });

  it("站台公開時未登入也能瀏覽，頂端列有「登入」", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(null),
      "GET /api/v1/versions": () => ({ versions: [f.item] }),
    });
    renderRoute("/");
    await screen.findAllByRole("article");
    expect(screen.getByRole("link", { name: "登入" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "帳號" })).toBeNull();
  });
});
