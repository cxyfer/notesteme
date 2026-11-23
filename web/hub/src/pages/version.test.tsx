import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import * as f from "../test/fixtures";
import { apiError, json, mockApi } from "../test/mockApi";
import { renderRoute, setCsrf } from "../test/render";

describe("版本頁", () => {
  it("顯示衍生關係、來源紀錄與下載連結；字幕以文字顯示", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/versions/{id}": () => f.version,
      "GET /api/v1/versions/{id}/download": () => new Response(f.cuesB),
    });
    renderRoute("/versions/v1");
    expect(await screen.findByText("以 @carol 的原始字幕重新生成")).toBeTruthy();
    expect(screen.getByRole("link", { name: "下載 SRT" }).getAttribute("href")).toBe(
      "/api/v1/versions/v1/download?format=srt",
    );
    expect(screen.getByRole("link", { name: "下載 CUES-JSON" })).toBeTruthy();
    expect(screen.getByText("parents_sha256")).toBeTruthy();
    // 字幕內容中的 HTML 只是文字
    expect(await screen.findByText("<img src=x onerror=alert(1)>")).toBeTruthy();
    expect(document.querySelector("img")).toBeNull();
    // 校正過的字幕不顯示「在 Client 重新生成」
    expect(screen.queryByText("在你的 Client 重新生成")).toBeNull();
  });

  it("原始字幕提供版本 ID 給 Client 重新生成；沒有下載權限時不顯示下載與預覽", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler({ ...f.member, permissions: ["read"] }),
      "GET /api/v1/versions/{id}": () => ({ ...f.version, variant: "source" }),
    });
    renderRoute("/versions/v1");
    expect(await screen.findByText("在你的 Client 重新生成")).toBeTruthy();
    expect(screen.getByRole("button", { name: "複製版本 ID" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: /^下載/ })).toBeNull();
    expect(screen.getAllByText("登入並有下載權限後才能下載與預覽內容。")).toHaveLength(2);
  });

  it("筆記不渲染原始 HTML（HREP-R18）", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/versions/{id}": () => ({ ...f.version, kind: "note", variant: "" }),
      "GET /api/v1/versions/{id}/download": () =>
        new Response("# Notes\n\n<script>alert(1)</script> **bold** [x](javascript:alert(1))"),
    });
    renderRoute("/versions/v1");
    expect(await screen.findByText("<script>alert(1)</script>", { exact: false })).toBeTruthy();
    expect(document.querySelector(".note-view script")).toBeNull();
    expect(document.querySelector(".note-view strong")?.textContent).toBe("bold");
    expect(document.querySelector(".note-view a[href^='javascript']")).toBeNull();
  });

  it("投票送出 PUT（帶 CSRF）並更新分數；自己的版本顯示錯誤", async () => {
    setCsrf("tok");
    let own = false;
    const api = mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/versions/{id}": () => f.version,
      "GET /api/v1/versions/{id}/download": () => new Response(f.cuesA),
      "PUT /api/v1/versions/{id}/vote": ({ body }) =>
        own
          ? apiError(403, "own_version", "不能對自己上傳的版本投票。")
          : { value: (body as { value: number }).value, score: 8 },
      "DELETE /api/v1/versions/{id}/vote": () => new Response(null, { status: 204 }),
    });
    renderRoute("/versions/v1");
    const up = await screen.findByRole("button", { name: "有幫助 +1" });
    await userEvent.click(up);
    expect(await screen.findByText("分數：8")).toBeTruthy();
    expect(up.getAttribute("aria-pressed")).toBe("true");
    const call = api.calls("PUT", "/api/v1/versions/v1/vote")[0]!;
    expect(new Headers(call.init.headers).get("X-CSRF-Token")).toBe("tok");
    expect(JSON.parse(String(call.init.body))).toEqual({ value: 1 });
    // 再按一次是收回
    await userEvent.click(up);
    await waitFor(() => expect(api.calls("DELETE", "/api/v1/versions/v1/vote")).toHaveLength(1));
    own = true;
    await userEvent.click(screen.getByRole("button", { name: "不好 −1" }));
    expect(await screen.findByText("不能對自己上傳的版本投票。")).toBeTruthy();
  });

  it("檢舉：選原因與說明送出；重複檢舉顯示 409 的說明", async () => {
    let n = 0;
    const api = mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/versions/{id}": () => f.version,
      "GET /api/v1/versions/{id}/download": () => new Response(f.cuesA),
      "POST /api/v1/versions/{id}/reports": () =>
        n++ === 0
          ? json(201, { id: "r1", state: "open" })
          : apiError(409, "already_reported", "你已經檢舉過這個版本，管理員處理前不能重複檢舉。"),
    });
    renderRoute("/versions/v1");
    await userEvent.click(await screen.findByRole("button", { name: "檢舉" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.selectOptions(within(dialog).getByLabelText("原因"), "spam");
    await userEvent.type(within(dialog).getByLabelText(/補充說明/), "ads");
    await userEvent.click(within(dialog).getByRole("button", { name: "送出檢舉" }));
    expect(await screen.findByText("已送出檢舉")).toBeTruthy();
    expect(
      JSON.parse(String(api.calls("POST", "/api/v1/versions/v1/reports")[0]!.init.body)),
    ).toEqual({
      reason: "spam",
      note: "ads",
    });
    await userEvent.click(screen.getByRole("button", { name: "檢舉" }));
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "送出檢舉" }),
    );
    expect(await screen.findByText(/你已經檢舉過這個版本/)).toBeTruthy();
  });

  it("下架申請的連結帶入版本 ID；版主可以隱藏版本", async () => {
    const api = mockApi({
      "GET /api/v1/me": f.meHandler(f.moderator),
      "GET /api/v1/versions/{id}": () => f.version,
      "GET /api/v1/versions/{id}/download": () => new Response(f.cuesA),
      "POST /api/v1/admin/versions/{id}/hide": () => ({ id: "v1", state: "hidden" }),
    });
    renderRoute("/versions/v1");
    const link = await screen.findByRole("link", { name: "提出下架申請" });
    expect(link.getAttribute("href")).toBe("/takedown?version=v1");
    await userEvent.click(screen.getByRole("button", { name: "隱藏這個版本" }));
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "隱藏這個版本" }),
    );
    await waitFor(() =>
      expect(api.calls("POST", "/api/v1/admin/versions/v1/hide")).toHaveLength(1),
    );
  });

  it("已移除的版本顯示 410 的說明", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/versions/{id}": () => apiError(410, "removed", "這個版本已依下架申請移除。"),
    });
    renderRoute("/versions/v1");
    expect(await screen.findByText("這個版本已依下架申請移除。")).toBeTruthy();
  });
});

describe("影片頁與比較", () => {
  it("依種類與語言分組；勾選兩個版本後並排比較，點一句兩邊一起跳", async () => {
    const api = mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/videos/{platform}/{external_id}": () => f.videoPage,
      "GET /api/v1/versions/{id}/download": ({ params }) =>
        new Response(params.id === "v1" ? f.cuesA : f.cuesB),
    });
    const { router } = renderRoute("/videos/youtube/abc123");
    // 標題是使用者上傳的文字：原樣顯示
    expect(await screen.findByRole("heading", { level: 1, name: "<b>Talk</b>" })).toBeTruthy();
    expect(screen.getByRole("heading", { level: 2, name: "字幕 · 校正 · en" })).toBeTruthy();
    const boxes = screen.getAllByRole("checkbox", { name: "比較" });
    await userEvent.click(boxes[0]!);
    expect(router.state.location.search).toEqual({});
    await userEvent.click(boxes[1]!);
    await waitFor(() => expect(router.state.location.search).toEqual({ compare: "v1,v2" }));
    await screen.findByRole("heading", { name: "並排比較" });
    expect(
      api
        .calls("GET", "/api/v1/versions/{id}/download")
        .map((c) => c.url.searchParams.get("format")),
    ).toEqual(["cues-json", "cues-json"]);
    // 時間帶是 web/shared 的 StatusBand（slider）
    expect(screen.getByRole("slider")).toBeTruthy();
    await userEvent.click(screen.getAllByRole("button", { name: /跳到 00:02/ })[0]!);
    await waitFor(() =>
      expect(document.querySelectorAll("li[aria-current='true'] .cue__text")[1]?.textContent).toBe(
        "<img src=x onerror=alert(1)>",
      ),
    );
    expect(screen.getByText("00:02 的兩句差異")).toBeTruthy();
  });

  it("找不到影片時顯示錯誤", async () => {
    mockApi({
      "GET /api/v1/me": f.meHandler(f.member),
      "GET /api/v1/videos/{platform}/{external_id}": () =>
        apiError(404, "not_found", "找不到這部影片。"),
    });
    renderRoute("/videos/youtube/zzz");
    expect(await screen.findByText("找不到這個項目，或你沒有權限查看。")).toBeTruthy();
  });
});
