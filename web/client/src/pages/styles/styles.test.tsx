import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { item } from "../../test/fixtures";
import { apiError, mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { style } from "../../test/tuneFixtures";
import { compactPatch, validatePatch } from "./styleModel";

const builtin = style("default", { builtin: true });
const custom = style("mine");

function stylesApi(extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /api/ass-styles": () => ({ styles: [builtin, style("large", { builtin: true }), custom] }),
    "GET /api/ass-styles/{id}": (req) =>
      [builtin, custom].find((s) => s.id === req.params.id) ??
      apiError(404, "not_found", "找不到這個樣式。"),
    "GET /api/items": () => ({ items: [item(1, { id: "item-1", title: "第一部" })], total: 1 }),
    ...extra,
  });
}

// jsdom 沒有 object URL
beforeEach(() => {
  let n = 0;
  URL.createObjectURL = vi.fn(() => `blob:preview-${++n}`);
  URL.revokeObjectURL = vi.fn();
});

describe("字幕樣式：模型", () => {
  it("只保留改過的欄位；顏色與範圍先在前端檢查", () => {
    expect(compactPatch({ primary: {}, layout: {} })).toBeUndefined();
    expect(compactPatch({ primary: { size: 60 }, translation: {} })).toEqual({
      primary: { size: 60 },
    });
    expect(
      validatePatch({
        primary: { color: "#FFF", size: 0 },
        translation: { outline_color: "#00000080", shadow: 51 },
        layout: { margin_v: 2000 },
      }),
    ).toEqual({
      "primary.color": "color",
      "primary.size": "range",
      "translation.shadow": "range",
      "layout.margin_v": "range",
    });
  });
});

describe("字幕樣式：列表", () => {
  it("內建在前、自訂在後；內建可以複製成自訂樣式（axe 無違規）", async () => {
    const api = stylesApi({
      "POST /api/ass-styles": (req) => {
        expect(req.body).toEqual({ from: "default", name: "default-custom" });
        return new Response(JSON.stringify(style("default-custom")), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        });
      },
    });
    signIn();
    const { container, router } = renderRoute("/styles");
    await screen.findByRole("heading", { level: 1, name: "字幕樣式" });
    const builtinList = within(
      (await screen.findByRole("heading", { name: "內建樣式" })).closest("section")!,
    );
    expect(builtinList.getAllByRole("listitem")).toHaveLength(2);
    const customList = within(
      screen.getByRole("heading", { name: "自訂樣式" }).closest("section")!,
    );
    expect(customList.getByRole("link", { name: "mine" })).toBeTruthy();
    await expectNoA11yViolations(container);

    await userEvent.click(builtinList.getAllByRole("button", { name: "複製成自訂樣式" })[0]!);
    const dialog = await screen.findByRole("dialog", { name: "複製 default" });
    await userEvent.click(within(dialog).getByRole("button", { name: "建立自訂樣式" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/styles/default-custom"));
    expect(api.calls("POST", "/api/ass-styles")).toHaveLength(1);
    expect(await screen.findByText("已建立 default-custom")).toBeTruthy();
  });

  it("匯入 .ass：讀取檔案內容送出", async () => {
    const api = stylesApi({
      "POST /api/ass-styles/import": (req) => {
        expect(req.body).toMatchObject({ name: "anime", content: "[V4+ Styles]\nStyle: Default" });
        return new Response(JSON.stringify(style("anime")), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        });
      },
    });
    signIn();
    const { router } = renderRoute("/styles");
    await userEvent.click(await screen.findByRole("button", { name: "匯入 .ass 樣式" }));
    const dialog = await screen.findByRole("dialog", { name: "匯入 .ass 樣式" });
    const file = new File(["[V4+ Styles]\nStyle: Default"], "Anime.ass", { type: "text/plain" });
    await userEvent.upload(within(dialog).getByLabelText(".ass／.ssa 檔"), file);
    expect((within(dialog).getByLabelText("名稱") as HTMLInputElement).value).toBe("anime");
    await userEvent.click(within(dialog).getByRole("button", { name: "匯入" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/styles/anime"));
    expect(api.calls("POST", "/api/ass-styles/import")).toHaveLength(1);
  });
});

describe("字幕樣式：編輯", () => {
  it("內建樣式唯讀，只能複製（axe 無違規）", async () => {
    stylesApi();
    signIn();
    const { container } = renderRoute("/styles/default");
    await screen.findByRole("heading", { level: 1, name: "default" });
    expect(await screen.findByRole("button", { name: "複製成自訂樣式" })).toBeTruthy();
    // 欄位在 disabled 的 fieldset 裡
    expect(screen.getByLabelText("大小").matches(":disabled")).toBe(true);
    expect(screen.queryByRole("button", { name: "儲存變更" })).toBeNull();
    await expectNoA11yViolations(container);
  });

  it("儲存只送改過的欄位；分頁可以用方向鍵切換", async () => {
    const api = stylesApi({
      "PATCH /api/ass-styles/{id}": (req) => ({ ...custom, ...(req.body as object) }),
    });
    signIn();
    renderRoute("/styles/mine");
    const size = (await screen.findByLabelText("大小")) as HTMLInputElement;
    const save = screen.getByRole("button", { name: "儲存變更" }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    fireEvent.change(size, { target: { value: "60" } });
    // 顏色格式錯誤時不能儲存
    fireEvent.change(screen.getByLabelText("文字"), { target: { value: "#FFF" } });
    expect(await screen.findByText("格式是 #RRGGBB 或 #RRGGBBAA。")).toBeTruthy();
    expect(save.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("文字"), { target: { value: "#FFFFFF80" } });

    const tab = screen.getByRole("tab", { name: "原文行" });
    tab.focus();
    await userEvent.keyboard("{ArrowRight}{ArrowRight}");
    expect(screen.getByRole("tab", { name: "版面" }).getAttribute("aria-selected")).toBe("true");
    await userEvent.selectOptions(screen.getByLabelText("位置"), "8");

    await userEvent.click(save);
    await waitFor(() => expect(api.calls("PATCH", "/api/ass-styles/mine")).toHaveLength(1));
    const body = JSON.parse(String(api.calls("PATCH", "/api/ass-styles/mine")[0]!.init.body));
    expect(body).toEqual({
      primary: { size: 60, color: "#FFFFFF80" },
      layout: { alignment: 8 },
    });
    expect(await screen.findByText("已儲存")).toBeTruthy();
  });

  it("預覽：選影音後延遲 300ms 才送出，連續修改只送最後一次；失敗時說明「預覽尚未可用」", async () => {
    let fail = false;
    const api = stylesApi({
      "POST /api/ass-styles/preview": () =>
        fail
          ? apiError(502, "upstream_failed", "ffmpeg 失敗")
          : new Response(new Blob(["png"], { type: "image/png" }), {
              status: 200,
              headers: { "Content-Type": "image/png" },
            }),
    });
    signIn();
    renderRoute("/styles/mine");
    expect(await screen.findByText(/選一部影音與時間/)).toBeTruthy();
    await screen.findByRole("option", { name: "第一部" });
    await userEvent.selectOptions(screen.getByLabelText("影音"), "item-1");
    const size = screen.getByLabelText("大小");
    fireEvent.change(size, { target: { value: "61" } });
    fireEvent.change(size, { target: { value: "62" } });
    fireEvent.change(size, { target: { value: "63" } });
    const img = await screen.findByRole("img", { name: /樣式 mine 在 00:00 的預覽畫面/ });
    expect(img.getAttribute("src")).toMatch(/^blob:preview-/);
    const calls = api.calls("POST", "/api/ass-styles/preview");
    expect(calls).toHaveLength(1);
    expect(JSON.parse(String(calls[0]!.init.body))).toMatchObject({
      style: "mine",
      item: "item-1",
      patch: { primary: { size: 63 } },
    });

    fail = true;
    fireEvent.change(screen.getByLabelText("畫面時間"), { target: { value: "06:25" } });
    expect(await screen.findByText(/預覽尚未可用/)).toBeTruthy();
    expect(screen.getByText(/平台暫時無法回應|ffmpeg/)).toBeTruthy();
    expect(screen.queryByRole("img", { name: /預覽畫面/ })).toBeNull();
    expect(api.calls("POST", "/api/ass-styles/preview")).toHaveLength(2);
  });

  it("找不到樣式時顯示 404 頁", async () => {
    stylesApi();
    signIn();
    renderRoute("/styles/nope");
    expect(await screen.findByRole("heading", { level: 1, name: "找不到這個頁面" })).toBeTruthy();
  });
});
