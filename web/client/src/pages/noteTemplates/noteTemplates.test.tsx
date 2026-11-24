import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { item } from "../../test/fixtures";
import { NOTE_MD, noteTemplate } from "../../test/m4Fixtures";
import { apiError, json, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";

/** CodeMirror 的編輯區（contenteditable，由 aria-labelledby 取名「模板內文」） */
function bodyEditor() {
  return screen.getByRole("textbox", { name: "模板內文" });
}

/** 取代編輯器全部內容（jsdom 沒有排版，直接改 DOM 讓 CodeMirror 讀取變更；同 prompts.test.tsx） */
async function typeBody(text: string) {
  const el = bodyEditor();
  el.textContent = text;
  await waitFor(() => expect(el.textContent).toBe(text));
}

function tplApi(extra: Record<string, MockHandler> = {}) {
  return mockApi({
    "GET /api/note-templates": () => ({
      templates: [noteTemplate("video-notes"), noteTemplate("mine", { version: 3 })],
    }),
    "GET /api/items": () => ({ items: [item(1), item(2)], total: 2 }),
    ...extra,
  });
}

describe("筆記模板：列表", () => {
  it("列出模板、內建標記與版本（axe 無違規）", async () => {
    tplApi();
    signIn();
    const { container } = renderRoute("/note-templates");
    await screen.findByRole("heading", { level: 1, name: "筆記模板" });
    expect(await screen.findByRole("link", { name: "Video Notes 預設" })).toBeTruthy();
    expect(screen.getByText("內建")).toBeTruthy();
    expect(screen.getByText("v3")).toBeTruthy();
    await expectNoA11yViolations(container);
  });

  it("複製內建模板：POST from，之後到編輯頁", async () => {
    const api = tplApi({
      "POST /api/note-templates": () => json(201, noteTemplate("my-video-notes")),
    });
    signIn();
    const user = userEvent.setup();
    const { router } = renderRoute("/note-templates");
    const row = (await screen.findByRole("link", { name: "Video Notes 預設" })).closest("li")!;
    await user.click(within(row).getByRole("button", { name: "複製" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "建立模板" }));
    await waitFor(() => expect(api.calls("POST", "/api/note-templates")).toHaveLength(1));
    expect(JSON.parse(String(api.calls("POST", "/api/note-templates")[0]!.init.body))).toEqual({
      id: "my-video-notes",
      from: "video-notes",
    });
    await waitFor(() =>
      expect(router.state.location.pathname).toBe("/note-templates/my-video-notes"),
    );
  });
});

describe("筆記模板：編輯", () => {
  it("內建模板唯讀，沒有儲存按鈕", async () => {
    tplApi();
    signIn();
    renderRoute("/note-templates/video-notes");
    await screen.findByRole("heading", { level: 1, name: "Video Notes 預設" });
    expect(bodyEditor().getAttribute("aria-readonly")).toBe("true");
    expect(screen.queryByRole("button", { name: "另存新版本" })).toBeNull();
  });

  it("只送改過的欄位；全部取消勾選時送 checks: []；422 顯示在內文旁（axe 無違規）", async () => {
    let n = 0;
    const api = tplApi({
      "PATCH /api/note-templates/{id}": () =>
        ++n === 1
          ? apiError(422, "invalid_request", "invalid", {
              errors: [{ path: "body", message: "不認識的變數 .Secret。" }],
            })
          : noteTemplate("mine", { version: 4, checks: [] }),
    });
    signIn();
    const user = userEvent.setup();
    const { container } = renderRoute("/note-templates/mine");
    await screen.findByRole("heading", { level: 1, name: "模板 mine" });
    await typeBody("{{.Secret}}");
    // 儲存之前就標出不認識的變數（NOTE 的 @ui 場景），樣式是 cm-var--unknown（波浪底線）
    expect(await screen.findByText("不認識的變數 .Secret。")).toBeTruthy();
    await waitFor(() =>
      expect(bodyEditor().querySelector(".cm-var--unknown")?.textContent).toBe(".Secret"),
    );
    await user.click(screen.getByRole("button", { name: "另存新版本" }));
    // 伺服器的 422 也顯示在內文旁
    await waitFor(() => expect(screen.getAllByText("不認識的變數 .Secret。")).toHaveLength(2));
    await expectNoA11yViolations(container);

    await typeBody(noteTemplate("mine").body);
    for (const box of screen.getAllByRole("checkbox"))
      if ((box as HTMLInputElement).checked) await user.click(box);
    await user.click(screen.getByRole("button", { name: "另存新版本" }));
    await waitFor(() => expect(api.calls("PATCH", "/api/note-templates/mine")).toHaveLength(2));
    expect(
      JSON.parse(String(api.calls("PATCH", "/api/note-templates/mine")[1]!.init.body)),
    ).toEqual({ checks: [] });
  });

  it("預覽送 <id>@<版本>，渲染 Markdown 與檢查；沒有筆記時說明", async () => {
    const api = tplApi({
      "POST /api/note-templates/{id}/preview": (req) =>
        (req.body as { item: string }).item === "item-2"
          ? apiError(409, "no_note", "沒有筆記")
          : {
              markdown: NOTE_MD,
              checks: [{ id: "self_check", ok: false, message: "缺少自我檢驗" }],
            },
    });
    signIn();
    const user = userEvent.setup();
    renderRoute("/note-templates/mine");
    await screen.findByRole("heading", { level: 1, name: "模板 mine" });
    const picker = screen.getByLabelText("影音");
    await waitFor(() =>
      expect(within(picker).getByRole("option", { name: "影片 1" })).toBeTruthy(),
    );
    await user.selectOptions(picker, "item-1");
    expect(await screen.findByText(/沒有通過：有「自我檢驗」段落/)).toBeTruthy();
    expect(screen.getByRole("region", { name: "預覽結果" })).toBeTruthy();
    expect(api.calls("POST", "/api/note-templates/mine%403/preview")).toHaveLength(1);
    await user.selectOptions(picker, "item-2");
    expect(await screen.findByText(/這部影音還沒有筆記/)).toBeTruthy();
  });

  it("有未儲存的修改時用草稿預覽（body、read_button、checks），不另存版本", async () => {
    const api = tplApi({
      "POST /api/note-templates/{id}/preview": () => ({ markdown: NOTE_MD, checks: [] }),
    });
    signIn();
    const user = userEvent.setup();
    renderRoute("/note-templates/mine");
    await screen.findByRole("heading", { level: 1, name: "模板 mine" });
    const picker = screen.getByLabelText("影音");
    await waitFor(() =>
      expect(within(picker).getByRole("option", { name: "影片 1" })).toBeTruthy(),
    );
    await user.selectOptions(picker, "item-1");
    await waitFor(() =>
      expect(api.calls("POST", "/api/note-templates/mine%403/preview")).toHaveLength(1),
    );
    expect(
      JSON.parse(String(api.calls("POST", "/api/note-templates/mine%403/preview")[0]!.init.body)),
    ).toEqual({
      item: "item-1",
    });

    await typeBody("# 草稿 {{.Title}}\n");
    expect(await screen.findByText("預覽的是編輯中的內容，還沒有儲存。")).toBeTruthy();
    await waitFor(
      () => expect(api.calls("POST", "/api/note-templates/mine%403/preview")).toHaveLength(2),
      { timeout: 3000 },
    );
    const draft = JSON.parse(
      String(api.calls("POST", "/api/note-templates/mine%403/preview")[1]!.init.body),
    );
    expect(draft.item).toBe("item-1");
    expect(draft.body).toBe("# 草稿 {{.Title}}\n");
    expect(draft).toHaveProperty("read_button");
    expect(Array.isArray(draft.checks)).toBe(true);
    expect(api.calls("PATCH", "/api/note-templates/mine")).toHaveLength(0);
  });
});
