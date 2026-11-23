import { screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../test/axe";
import { defaultRoutes } from "../test/fixtures";
import { apiError, mockApi } from "../test/mockApi";
import { renderRoute, signIn } from "../test/render";

function setup() {
  return mockApi({
    ...defaultRoutes(),
    "POST /api/resolve": () => apiError(422, "unrecognized_url", "無法辨識"),
  });
}

describe("/add（ENTRY-R2）", () => {
  beforeEach(() => signIn());

  it("打開「新增」抽屜並帶入網址，照常辨識但不開始處理", async () => {
    const api = setup();
    const { router } = renderRoute("/add?url=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx");
    const input = await screen.findByDisplayValue("https://youtu.be/abc123xxxxx");
    expect(input).toBeTruthy();
    await waitFor(() => expect(api.calls("POST", "/api/resolve")).toHaveLength(1));
    expect(JSON.parse(String(api.calls("POST", "/api/resolve")[0]!.init.body))).toEqual({
      url: "https://youtu.be/abc123xxxxx",
    });
    expect(api.calls("POST", "/api/items")).toHaveLength(0);
    // 換成媒體庫，重新整理不會再開一次
    expect(router.state.location.pathname).toBe("/library");
    expect(router.state.location.searchStr).toBe("");
    expect(screen.queryByRole("status", { name: /無效/ })).toBeNull();
  });

  it("不是 http／https 的網址：輸入框留空並提示", async () => {
    const api = setup();
    renderRoute("/add?url=javascript%3Aalert(1)");
    expect(await screen.findByText("書籤或分享的網址無效，已忽略。")).toBeTruthy();
    const dialog = await screen.findByRole("dialog");
    expect((dialog.querySelector('input[type="url"]') as HTMLInputElement).value).toBe("");
    expect(api.calls("POST", "/api/resolve")).toHaveLength(0);
    await expectNoA11yViolations(dialog);
  });

  it("分享的內容沒有網址", async () => {
    setup();
    renderRoute("/add?share_error=no_url");
    expect(await screen.findByText("分享的內容中沒有網址。")).toBeTruthy();
  });
});
