import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes, detail, item, stage } from "../../test/fixtures";
import { apiError, json, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";

const PAUSED = () => ({
  ...detail(item(1), [
    stage("metadata", "done"),
    stage("transcript", "done"),
    stage("normalize", "done"),
    stage("summary", "done"),
    stage("segment", "blocked", {
      block_reason: "awaiting_user",
      message: "摘要完成，等你決定要不要繼續處理。",
      actions: ["繼續處理", "取消"],
    }),
  ]),
  summary: {
    language: "zh-Hant",
    text: "第一段。\n<b>第二段</b>",
    key_points: ["重點一", "重點二"],
    title_translation: "影片一",
  },
});

function itemApi(extra: Record<string, MockHandler> = {}) {
  return mockApi({ ...defaultRoutes(), "GET /api/items/{id}": PAUSED, ...extra });
}

describe("影音詳情：摘要與繼續（DIG）", () => {
  it("顯示摘要、譯名與重點（純文字、帶 lang）；暫停時可以繼續（axe 無違規）", async () => {
    const api = itemApi({
      "POST /api/items/{id}/continue": () => json(202, { continued: "segment" }),
    });
    signIn();
    const { container } = renderRoute("/items/item-1");
    const block = await screen.findByRole("region", { name: "摘要" });
    expect(within(block).getByText("影片一")).toBeTruthy();
    expect(within(block).getByText(/<b>第二段<\/b>/)).toBeTruthy();
    expect(block.querySelector("b")).toBeNull();
    expect(block.querySelector("[lang='zh-Hant']")).toBeTruthy();
    expect(within(block).getAllByRole("listitem")).toHaveLength(2);
    await expectNoA11yViolations(container);
    await userEvent.setup().click(within(block).getByRole("button", { name: "繼續處理" }));
    expect(await screen.findByText("已繼續處理，從分段開始")).toBeTruthy();
    expect(api.calls("POST", "/api/items/item-1/continue")).toHaveLength(1);
  });

  it("步驟分頁的「繼續處理」「取消」對應到動作；409 顯示訊息", async () => {
    itemApi({
      "POST /api/items/{id}/continue": () =>
        apiError(409, "not_awaiting_user", "這部影音沒有在等待你決定是否繼續。"),
    });
    signIn();
    renderRoute("/items/item-1?tab=stages");
    const user = userEvent.setup();
    const seg = (await screen.findByRole("heading", { level: 3, name: "分段" })).closest("li")!;
    expect(within(seg).queryByRole("button", { name: /重新處理/ })).toBeNull();
    await user.click(within(seg).getByRole("button", { name: "取消處理" }));
    expect(await screen.findByRole("dialog")).toBeTruthy();
    await user.keyboard("{Escape}");
    await user.click(within(seg).getByRole("button", { name: "繼續處理" }));
    expect(await screen.findByText("這部影音沒有在等待你決定是否繼續。")).toBeTruthy();
  });

  it("沒有摘要也沒有暫停時不顯示摘要區塊", async () => {
    mockApi(defaultRoutes());
    signIn();
    renderRoute("/items/item-1");
    await screen.findByRole("heading", { level: 1, name: "影片 1" });
    await waitFor(() => expect(screen.queryByRole("region", { name: "摘要" })).toBeNull());
  });
});
