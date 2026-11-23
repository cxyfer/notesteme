import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes } from "../../test/fixtures";
import { framesList, noteBody } from "../../test/m4Fixtures";
import { apiError, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { clockSeconds } from "./NotePanels";

function itemApi(extra: Record<string, MockHandler> = {}) {
  return mockApi({
    ...defaultRoutes(),
    "GET /api/items/{id}/frames": () => framesList(),
    "GET /api/items/{id}/note": () => noteBody(),
    ...extra,
  });
}

async function openTab(tab: string) {
  signIn();
  const r = renderRoute(`/items/item-1?tab=${tab}`);
  await screen.findByRole("heading", { level: 1, name: "影片 1" });
  return r;
}

describe("影音詳情：畫面", () => {
  it("clockSeconds 解析 HH:MM:SS.mmm", () => {
    expect(clockSeconds("01:02:03.500")).toBe(3723.5);
    expect(clockSeconds("bad")).toBe(0);
  });

  it("預設只看選中的畫面，可切換全部候選；圖說是純文字（axe 無違規）", async () => {
    const api = itemApi();
    const user = userEvent.setup();
    const { container } = await openTab("frames");
    const sheet = await screen.findByRole("list", { name: "印樣" });
    expect(within(sheet).getAllByRole("img")).toHaveLength(2);
    expect(within(sheet).getByRole("img", { name: "架構圖" }).getAttribute("src")).toBe(
      "/api/items/item-1/frames/1?size=thumb",
    );
    expect(screen.getByText(/選中 2 張（目標 2，建議 2～4 張），共 3 張候選/)).toBeTruthy();
    await expectNoA11yViolations(container);
    await user.click(screen.getByRole("button", { name: "全部候選" }));
    expect(within(sheet).getAllByRole("img")).toHaveLength(3);
    expect(within(sheet).getByText(/在 SponsorBlock 片段內/)).toBeTruthy();
    // 其他分頁不會讀畫面與筆記
    expect(api.calls("GET", "/api/items/{id}/note")).toHaveLength(0);
  });

  it("沒有執行過畫面步驟時顯示說明", async () => {
    itemApi({ "GET /api/items/{id}/frames": () => apiError(404, "no_frames", "沒有畫面") });
    await openTab("frames");
    expect(await screen.findByText(/還沒有擷取畫面/)).toBeTruthy();
  });

  it("字幕分頁不讀畫面與筆記", async () => {
    const api = itemApi();
    await openTab("subtitles");
    await screen.findAllByText("第一句");
    expect(api.calls("GET", "/api/items/{id}/frames")).toHaveLength(0);
    expect(api.calls("GET", "/api/items/{id}/note")).toHaveLength(0);
  });
});

describe("影音詳情：筆記", () => {
  it("渲染筆記、檢查、寫入狀態；危險連結不產生連結（axe 無違規）", async () => {
    itemApi();
    const { container } = await openTab("note");
    const note = await screen.findByRole("region", { name: "筆記內容" });
    expect(within(note).getByRole("heading", { name: /開場/ })).toBeTruthy();
    expect(within(note).getByText(/<script>alert\(1\)<\/script>/)).toBeTruthy();
    expect(within(note).queryByRole("link", { name: "連結" })).toBeNull();
    expect(within(note).getByRole("img", { name: "畫面 1" })).toBeTruthy();
    expect(screen.getByText(/沒有通過：每個內容標題都有時間戳/)).toBeTruthy();
    expect(screen.getByText("Video Notes/影片 1.md")).toBeTruthy();
    expect(screen.getByRole("link", { name: "下載 Markdown" }).getAttribute("href")).toBe(
      "/api/items/item-1/note?format=md",
    );
    await expectNoA11yViolations(container);
  });

  it("過時的筆記提供「重新產生筆記」，打開重新處理對話框", async () => {
    itemApi({
      "GET /api/items/{id}/note": () =>
        noteBody({ stale: true, stale_reason: "字幕已更新，筆記可能過時。" }),
    });
    const user = userEvent.setup();
    await openTab("note");
    await user.click(await screen.findByRole("button", { name: "重新產生筆記" }));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeTruthy());
  });

  it("沒有筆記時顯示說明", async () => {
    itemApi({ "GET /api/items/{id}/note": () => apiError(404, "no_note", "沒有筆記") });
    await openTab("note");
    expect(await screen.findByText(/這部影音還沒有筆記/)).toBeTruthy();
  });
});
