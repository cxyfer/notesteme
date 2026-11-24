import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
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

// IntersectionObserver 的替身：測試決定哪些縮圖進入視窗
class FakeIO {
  static all: FakeIO[] = [];
  els: Element[] = [];
  constructor(private cb: IntersectionObserverCallback) {
    FakeIO.all.push(this);
  }
  observe(el: Element) {
    this.els.push(el);
  }
  disconnect() {
    this.els = [];
  }
  unobserve() {}
  takeRecords() {
    return [];
  }
  static show(el: Element) {
    for (const io of FakeIO.all.filter((x) => x.els.includes(el)))
      io.cb(
        [{ isIntersecting: true, target: el } as unknown as IntersectionObserverEntry],
        io as unknown as IntersectionObserver,
      );
  }
}

const png = () =>
  new Response(new Blob(["png"], { type: "image/png" }), {
    status: 200,
    headers: { "Content-Type": "image/png" },
  });

describe("字幕樣式：內建樣式比較（pages.md §9）", () => {
  beforeEach(() => {
    FakeIO.all = [];
    vi.stubGlobal("IntersectionObserver", FakeIO);
  });

  it("選了影音才渲染；進入視窗才送出，一次一張（axe 無違規）", async () => {
    const pending: (() => void)[] = [];
    const api = stylesApi({
      "POST /api/ass-styles/preview": () =>
        new Promise<Response>((resolve) => pending.push(() => resolve(png()))),
    });
    signIn();
    const { container } = renderRoute("/styles");
    const section = await screen.findByRole("region", { name: "內建樣式比較" });
    expect(within(section).getByText("選一部影音來比較內建樣式。")).toBeTruthy();
    await userEvent.selectOptions(await within(section).findByLabelText("影音"), "item-1");
    const cells = within(section).getAllByRole("listitem");
    expect(cells.map((c) => within(c).getByRole("figure").textContent)).toEqual([
      expect.stringContaining("default"),
      expect.stringContaining("large"),
    ]);
    // 還沒進入視窗：不送任何預覽
    expect(api.calls("POST", "/api/ass-styles/preview")).toHaveLength(0);
    act(() => {
      FakeIO.show(cells[0]!);
      FakeIO.show(cells[1]!);
    });
    await waitFor(() => expect(api.calls("POST", "/api/ass-styles/preview")).toHaveLength(1));
    expect(JSON.parse(String(api.calls("POST", "/api/ass-styles/preview")[0]!.init.body))).toEqual({
      style: "default",
      item: "item-1",
    });
    // 前一張完成才送下一張
    await act(async () => pending.shift()!());
    await waitFor(() => expect(api.calls("POST", "/api/ass-styles/preview")).toHaveLength(2));
    expect(
      JSON.parse(String(api.calls("POST", "/api/ass-styles/preview")[1]!.init.body)).style,
    ).toBe("large");
    await act(async () => pending.shift()!());
    expect(await within(section).findByRole("img", { name: "「large」樣式的預覽" })).toBeTruthy();
    expect(within(section).getByRole("img", { name: "「default」樣式的預覽" })).toBeTruthy();
    await expectNoA11yViolations(container);
  });

  it("失敗的縮圖顯示原因，不擋下一張", async () => {
    stylesApi({
      "POST /api/ass-styles/preview": (req) =>
        (req.body as { style: string }).style === "default"
          ? apiError(502, "upstream_failed", "ffmpeg 失敗")
          : png(),
    });
    vi.stubGlobal("IntersectionObserver", undefined);
    signIn();
    renderRoute("/styles");
    const section = await screen.findByRole("region", { name: "內建樣式比較" });
    await userEvent.selectOptions(await within(section).findByLabelText("影音"), "item-1");
    expect(await within(section).findByRole("img", { name: "「large」樣式的預覽" })).toBeTruthy();
    expect(within(section).getByText(/預覽尚未可用/)).toBeTruthy();
  });

  it("StrictMode 的模擬卸載後仍會送出第一張", async () => {
    vi.stubGlobal("IntersectionObserver", undefined);
    const api = stylesApi({ "POST /api/ass-styles/preview": () => png() });
    const { BuiltinCompare } = await import("./BuiltinCompare");
    const { render } = await import("@testing-library/react");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { StrictMode } = await import("react");
    render(
      <StrictMode>
        <QueryClientProvider client={new QueryClient()}>
          <BuiltinCompare styles={["default", "large"]} />
        </QueryClientProvider>
      </StrictMode>,
    );
    await screen.findByRole("option", { name: "第一部" });
    await userEvent.selectOptions(screen.getByLabelText("影音"), "item-1");
    expect(await screen.findByRole("img", { name: "「large」樣式的預覽" })).toBeTruthy();
    expect(screen.getByRole("img", { name: "「default」樣式的預覽" })).toBeTruthy();
    expect(api.calls("POST", "/api/ass-styles/preview").length).toBeGreaterThanOrEqual(2);
  });

  it("媒體庫沒有影音時顯示空狀態，沒有選擇器", async () => {
    stylesApi({ "GET /api/items": () => ({ items: [], total: 0 }) });
    signIn();
    renderRoute("/styles");
    const section = await screen.findByRole("region", { name: "內建樣式比較" });
    expect(await within(section).findByText(/媒體庫還沒有影音/)).toBeTruthy();
    expect(within(section).queryByLabelText("影音")).toBeNull();
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
