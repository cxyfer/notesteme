import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { item } from "../../test/fixtures";
import { apiError, json, mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { profile, promptSummary, promptVersion } from "../../test/tuneFixtures";
import { groupOf, scanVariables } from "./promptModel";

describe("Prompt：模型", () => {
  it("依 kind 分組，不認得的歸入其他", () => {
    expect(groupOf("segment")).toBe("segment");
    expect(groupOf("frames.select")).toBe("frames");
    expect(groupOf("note_map")).toBe("note");
    expect(groupOf("chapters")).toBe("other");
    expect(groupOf("something-new")).toBe("other");
  });

  it("變數：已知、未定義、格式錯誤、未關閉（與後端規則一致）", () => {
    const body = "{{ title }} {{nope}} {{a b}} {{glossary}} {{oops";
    const { tokens, issues } = scanVariables(body);
    expect(tokens.map((t) => [t.name, t.known])).toEqual([
      ["title", true],
      ["nope", false],
      ["glossary", true],
    ]);
    expect(issues.map((i) => [i.kind, i.name])).toEqual([
      ["undefined", "nope"],
      ["invalid", "a b"],
      ["unclosed", ""],
    ]);
    expect(body.slice(issues[0]!.from, issues[0]!.to)).toBe("{{nope}}");
  });
});

const mine = promptSummary("my-correct", { default_version: 2, latest_version: 2 });

function promptsApi(extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /api/prompts": () => ({
      prompts: [
        promptSummary("correct-default", { builtin: true }),
        promptSummary("segment-default", { builtin: true, kind: "segment" }),
        promptSummary("my-chapters", { kind: "chapters" }),
        mine,
      ],
    }),
    "GET /api/prompts/{id}/versions": (req) =>
      req.params.id === "my-correct"
        ? {
            id: "my-correct",
            versions: [
              promptVersion(1, "校對 {{title}}"),
              promptVersion(2, "只修正錯字。{{glossary}}"),
            ],
          }
        : req.params.id === "correct-default"
          ? { id: "correct-default", versions: [promptVersion(1, "內建 {{glossary}}")] }
          : apiError(404, "not_found", "找不到"),
    "GET /api/profiles": () => ({
      profiles: [
        profile("standard", { config: { stages: { correct: { prompt: "my-correct@2" } } } }),
      ],
    }),
    "GET /api/items": () => ({ items: [item(1, { id: "item-1", title: "第一部" })], total: 1 }),
    ...extra,
  });
}

async function openEditor(id = "my-correct") {
  signIn();
  const r = renderRoute(`/prompts/${id}`);
  await screen.findByRole("heading", { level: 1, name: id });
  await screen.findByRole("group", { name: "版本" });
  return r;
}

/** CodeMirror 的編輯區（contenteditable，由 aria-labelledby 取名） */
function editor() {
  return screen.getByRole("textbox", { name: /內容（v\d+）/ });
}

/** 取代編輯器全部內容（jsdom 沒有排版，直接改 DOM 讓 CodeMirror 讀取變更） */
async function typeInEditor(text: string) {
  const el = editor();
  el.textContent = text;
  await waitFor(() => expect(el.textContent).toBe(text));
}

describe("Prompt 庫：列表", () => {
  it("依用途分組，顯示目前版本與使用中的處理方案（axe 無違規）", async () => {
    promptsApi();
    signIn();
    const { container } = renderRoute("/prompts");
    const correct = within(
      (await screen.findByRole("heading", { name: "校正" })).closest("section")!,
    );
    expect(correct.getByRole("link", { name: "correct-default" })).toBeTruthy();
    expect(await correct.findByRole("link", { name: "standard" })).toBeTruthy();
    expect(
      within(screen.getByRole("heading", { name: "分段" }).closest("section")!).getByText(
        "segment-default",
      ),
    ).toBeTruthy();
    expect(
      within(screen.getByRole("heading", { name: "其他" }).closest("section")!).getByText(
        "my-chapters",
      ),
    ).toBeTruthy();
    await expectNoA11yViolations(container);
  });
});

describe("Prompt 庫：編輯", () => {
  it("顯示預設版本的內容，變數清單與版本列（axe 無違規）", async () => {
    promptsApi();
    const { container } = await openEditor();
    expect(screen.getByRole("button", { name: "v2（使用中）" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    expect(editor().textContent).toContain("只修正錯字。");
    expect(screen.getByText("glossary")).toBeTruthy();
    await expectNoA11yViolations(container);
  });

  it("內建的 prompt 唯讀，只能另存為新的 prompt", async () => {
    promptsApi();
    await openEditor("correct-default");
    expect(editor().getAttribute("aria-readonly")).toBe("true");
    expect(editor().getAttribute("contenteditable")).toBe("false");
    expect(screen.queryByRole("button", { name: "另存新版本" })).toBeNull();
    expect(screen.queryByRole("button", { name: "刪除 prompt" })).toBeNull();
    expect(screen.getByRole("button", { name: "另存為新的 prompt" })).toBeTruthy();
  });

  it("另存新版本：未定義的變數先擋下，修正後送出並提示「已存為 v3」", async () => {
    const api = promptsApi({
      "POST /api/prompts/{id}/versions": (req) =>
        json(201, { id: "my-correct", version: 3, sha256: "x", body: req.body }),
    });
    await openEditor();
    const save = screen.getByRole("button", { name: "另存新版本" }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    await typeInEditor("修正 {{nope}}");
    expect(await screen.findByText(/未定義的變數「nope」/)).toBeTruthy();
    expect(save.disabled).toBe(true);
    await typeInEditor("修正 {{title}}");
    await waitFor(() => expect(save.disabled).toBe(false));
    await userEvent.click(save);
    expect(await screen.findByText("已存為 v3")).toBeTruthy();
    expect(
      JSON.parse(String(api.calls("POST", "/api/prompts/my-correct/versions")[0]!.init.body)),
    ).toEqual({
      body: "修正 {{title}}",
    });
  });

  it("設為預設：送出選擇的版本", async () => {
    const api = promptsApi({
      "POST /api/prompts/{id}/default": () => ({ id: "my-correct", default_version: 1 }),
    });
    await openEditor();
    const setDefault = screen.getByRole("button", { name: "設為預設" }) as HTMLButtonElement;
    expect(setDefault.disabled).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "v1" }));
    expect(editor().textContent).toContain("校對");
    await userEvent.click(setDefault);
    expect(await screen.findByText("已設為預設")).toBeTruthy();
    expect(
      JSON.parse(String(api.calls("POST", "/api/prompts/my-correct/default")[0]!.init.body)),
    ).toEqual({
      version: 1,
    });
  });

  it("刪除：先確認；仍被使用時列出使用者，不刪除", async () => {
    const api = promptsApi({
      "DELETE /api/prompts/{id}": () =>
        apiError(409, "prompt_in_use", "仍被使用", {
          used_by: ["standard", "settings", "channel:youtube:UC1", "item:yt:abc"],
        }),
    });
    await openEditor();
    await userEvent.click(screen.getByRole("button", { name: "刪除 prompt" }));
    const dialog = await screen.findByRole("dialog", { name: "刪除 my-correct？" });
    expect(within(dialog).getByText(/2 個版本，無法復原/)).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "永久刪除" }));
    expect(await within(dialog).findByText(/仍被使用，所以沒有刪除/)).toBeTruthy();
    expect(within(dialog).getByRole("link", { name: "處理方案 standard" })).toBeTruthy();
    expect(within(dialog).getByText("全域設定（步驟）")).toBeTruthy();
    expect(within(dialog).getByText("頻道覆寫 youtube:UC1")).toBeTruthy();
    expect(within(dialog).getByRole("link", { name: "影音覆寫 yt:abc" })).toBeTruthy();
    expect(within(dialog).queryByRole("button", { name: "永久刪除" })).toBeNull();
    expect(api.calls("DELETE", "/api/prompts/my-correct")).toHaveLength(1);
  });

  it("刪除成功後回到列表", async () => {
    promptsApi({ "DELETE /api/prompts/{id}": () => new Response(null, { status: 204 }) });
    const { router } = await openEditor();
    await userEvent.click(screen.getByRole("button", { name: "刪除 prompt" }));
    await userEvent.click(await screen.findByRole("button", { name: "永久刪除" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/prompts"));
    expect(await screen.findByText("已刪除 my-correct")).toBeTruthy();
  });

  async function fillRun() {
    await screen.findByRole("option", { name: "第一部" });
    const panel = within(screen.getByRole("heading", { name: "試跑" }).closest("section")!);
    await userEvent.selectOptions(panel.getByLabelText("影音"), "item-1");
    fireEvent.change(panel.getByLabelText("範圍開始"), { target: { value: "06:00" } });
    fireEvent.change(panel.getByLabelText("範圍結束"), { target: { value: "08:00" } });
    fireEvent.change(panel.getByLabelText("Provider"), { target: { value: "anthropic" } });
    fireEvent.change(panel.getByLabelText("模型"), { target: { value: "m1" } });
    return panel;
  }

  it("試跑：以差異模式顯示輸出與驗證結果，花費未知時明說", async () => {
    const api = promptsApi({
      "POST /api/prompts/playground": () => ({
        stage: "correct",
        prompt: { id: "my-correct", version: 2, sha256: "x" },
        cues: 1,
        output: [
          {
            index: 0,
            start_ms: 385000,
            end_ms: 388000,
            text: "比如說 daily",
            original: "比如说daily",
          },
        ],
        validation: { passed: true, errors: [] },
        usage: { input_tokens: 120, output_tokens: 30 },
        estimate_usd: null,
      }),
    });
    await openEditor();
    const panel = await fillRun();
    await userEvent.click(panel.getByRole("button", { name: "試跑" }));
    expect(await panel.findByText("驗證通過：1 句")).toBeTruthy();
    expect(panel.getByText("06:25")).toBeTruthy();
    expect(panel.getByText(/計價表沒有這個模型的價格/)).toBeTruthy();
    const del = panel.getByText((_, el) => el?.tagName === "DEL");
    expect(del.textContent).toBe("刪除：说");
    expect(JSON.parse(String(api.calls("POST", "/api/prompts/playground")[0]!.init.body))).toEqual({
      prompt: "my-correct@2",
      item: "item-1",
      from: "06:00",
      to: "08:00",
      provider: "anthropic",
      model: "m1",
    });
  });

  it("試跑：這個類型還不能試跑（501）時說明原因", async () => {
    promptsApi({
      "POST /api/prompts/playground": () =>
        apiError(501, "not_implemented", "segment 的試跑還沒有實作"),
    });
    await openEditor();
    const panel = await fillRun();
    await userEvent.click(panel.getByRole("button", { name: "試跑" }));
    expect(await panel.findByText(/還不能試跑（segment 的試跑還沒有實作）/)).toBeTruthy();
  });

  it("比較版本：顯示逐行差異", async () => {
    const api = promptsApi({
      "POST /api/prompts/{id}/compare": () => ({
        a: 1,
        b: 2,
        body_diff: [
          { op: "delete", text: "校對 {{title}}" },
          { op: "insert", text: "只修正錯字。{{glossary}}" },
        ],
      }),
    });
    await openEditor();
    await userEvent.click(screen.getByRole("button", { name: "比較版本" }));
    const dialog = await screen.findByRole("dialog", { name: "比較 my-correct 的版本" });
    await userEvent.click(within(dialog).getByRole("button", { name: "比較" }));
    const diff = await within(dialog).findByRole("list", { name: "內容的逐行差異" });
    expect(within(diff).getAllByRole("listitem")).toHaveLength(2);
    expect(within(diff).getByText("新增：")).toBeTruthy();
    expect(
      JSON.parse(String(api.calls("POST", "/api/prompts/my-correct/compare")[0]!.init.body)),
    ).toEqual({
      a: 1,
      b: 2,
    });
  });
});
