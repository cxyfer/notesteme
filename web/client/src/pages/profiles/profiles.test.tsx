import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { apiError, mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { profile, promptSummary, style } from "../../test/tuneFixtures";
import { mergePatch, promptUsers, setPath, stripNulls } from "./profileModel";

describe("處理方案：模型", () => {
  it("mergePatch：刪掉的鍵送 null，物件遞迴，陣列整個取代；沒有差異時是 undefined", () => {
    const before = {
      stages: { correct: { executor: "api", provider: "anthropic", model: "m" } },
      outputs: [{ target: "sidecar", formats: ["srt"] }],
      llm: { max_validation_retries: 2 },
    };
    expect(mergePatch(before, structuredClone(before))).toBeUndefined();
    const after = {
      stages: { correct: { executor: "agent" } },
      outputs: [{ target: "sidecar", formats: ["srt", "ass"] }],
      llm: { max_validation_retries: 2 },
    };
    expect(mergePatch(before, after)).toEqual({
      stages: { correct: { executor: "agent", provider: null, model: null } },
      outputs: [{ target: "sidecar", formats: ["srt", "ass"] }],
    });
  });

  it("setPath 刪除鍵時一併刪掉變空的物件；stripNulls 去掉 null", () => {
    const doc = { stages: { correct: { executor: "api" } }, timing: { split_timing: "free" } };
    expect(setPath(doc, "stages.correct.executor", undefined)).toEqual({
      timing: { split_timing: "free" },
    });
    expect(setPath({}, "language.targets", ["en"])).toEqual({ language: { targets: ["en"] } });
    expect(stripNulls({ a: null, b: { c: null, d: 1 } })).toEqual({ b: { d: 1 } });
  });

  it("promptUsers 解析 <id>、<id>@default、<id>@N", () => {
    const users = promptUsers([
      profile("b", { config: { stages: { correct: { prompt: "my-correct@2" } } } }),
      profile("a", { config: { stages: { correct: { prompt: "my-correct" } } } }),
      profile("c", { config: { stages: { translate: { prompt: "tr@default" } } } }),
    ]);
    expect(users.get("my-correct")).toEqual(["a", "b"]);
    expect(users.get("tr")).toEqual(["c"]);
  });
});

const standard = profile("standard", {
  version: 5,
  config: {
    stages: {
      correct: { executor: "api", provider: "anthropic", model: "m1", prompt: "my-correct@1" },
      translate: { executor: "off" },
    },
    outputs: [{ target: "sidecar", formats: ["srt"] }],
    llm: { max_validation_retries: 2 },
  },
});

function profilesApi(extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /api/profiles": () => ({
      profiles: [profile("default", { builtin: true, default: true }), standard],
    }),
    "GET /api/profiles/{id}": (req) =>
      req.params.id === "standard"
        ? standard
        : req.params.id === "default"
          ? profile("default", { builtin: true, default: true })
          : apiError(404, "not_found", "找不到"),
    "GET /api/prompts": () => ({
      prompts: [
        promptSummary("correct-default", { builtin: true }),
        promptSummary("my-correct", { default_version: 2, latest_version: 3 }),
      ],
    }),
    "GET /api/ass-styles": () => ({ styles: [style("default", { builtin: true })] }),
    ...extra,
  });
}

async function openStandard() {
  signIn();
  const r = renderRoute("/profiles/standard");
  await screen.findByRole("heading", { level: 1, name: "standard" });
  // 載入完成後才有流程列
  await screen.findByRole("list", { name: "處理流程" });
  return r;
}

describe("處理方案：列表", () => {
  it("列出處理方案、預設標記與各步驟的執行方式（axe 無違規）", async () => {
    profilesApi();
    signIn();
    const { container } = renderRoute("/profiles");
    await screen.findByRole("heading", { level: 1, name: "處理方案" });
    expect(await screen.findByRole("link", { name: "standard" })).toBeTruthy();
    expect(screen.getByText("預設")).toBeTruthy();
    expect(screen.getByText(/校正 API 處理，翻譯 不執行/)).toBeTruthy();
    await expectNoA11yViolations(container);
  });
});

describe("處理方案：編輯", () => {
  it("流程列標出不執行的步驟；欄位反映設定（axe 無違規）", async () => {
    profilesApi();
    const { container } = await openStandard();
    const flow = screen.getByRole("list", { name: "處理流程" });
    expect(within(flow).getByText("翻譯").closest("li")!.className).toContain("flow__step--off");
    const correct = within(screen.getByRole("heading", { name: /校正/ }).closest("li")!);
    expect((correct.getByLabelText("模型") as HTMLInputElement).value).toBe("m1");
    expect((correct.getByLabelText("Prompt 版本") as HTMLSelectElement).value).toBe("1");
    await expectNoA11yViolations(container);
  });

  it("儲存送 merge patch；版本一致時直接儲存", async () => {
    const api = profilesApi({
      "PATCH /api/profiles/{id}": () => ({ ...standard, version: 6 }),
    });
    await openStandard();
    const correct = within(screen.getByRole("heading", { name: /校正/ }).closest("li")!);
    fireEvent.change(correct.getByLabelText("模型"), { target: { value: "m2" } });
    await userEvent.selectOptions(correct.getByLabelText("Prompt 版本"), "");
    await userEvent.click(screen.getByRole("button", { name: "儲存變更" }));
    expect(await screen.findByText("已儲存（v6）")).toBeTruthy();
    const body = JSON.parse(String(api.calls("PATCH", "/api/profiles/standard")[0]!.init.body));
    expect(body).toEqual({
      config: { stages: { correct: { model: "m2", prompt: "my-correct" } } },
    });
  });

  it("輸出目標可以選 vault：只能選用設定檔的 vault 與 hook，換目標時丟掉不適用的欄位", async () => {
    const api = profilesApi({
      "GET /api/vaults": () => ({ vaults: [{ id: "main", adapter: "fs", base: "Video Notes" }] }),
      "GET /api/hooks": () => ({ hooks: [{ id: "notify" }] }),
      "PATCH /api/profiles/{id}": () => ({ ...standard, version: 6 }),
    });
    await openStandard();
    const card = within(screen.getByRole("group", { name: "輸出 1" }));
    expect(card.getByLabelText("字幕樣式（ASS）")).toBeTruthy();
    await userEvent.selectOptions(card.getByLabelText("輸出目標"), "vault");
    // vault 預設選第一個；沒有字幕樣式；hook 只能勾選設定檔定義的
    await waitFor(() =>
      expect((card.getByLabelText("Vault") as HTMLSelectElement).value).toBe("main"),
    );
    expect(card.queryByLabelText("字幕樣式（ASS）")).toBeNull();
    await userEvent.click(card.getByRole("checkbox", { name: "notify" }));
    await userEvent.click(screen.getByRole("button", { name: "儲存變更" }));
    expect(await screen.findByText("已儲存（v6）")).toBeTruthy();
    const body = JSON.parse(String(api.calls("PATCH", "/api/profiles/standard")[0]!.init.body));
    expect(body.config.outputs).toEqual([
      { target: "vault", formats: ["srt"], vault: "main", hooks: ["notify"] },
    ]);
  });

  it("儲存前發現版本已改變：提示衝突，可以重新載入或仍要儲存", async () => {
    const latest = 7;
    const api = profilesApi({
      "PATCH /api/profiles/{id}": () => ({ ...standard, version: 8 }),
    });
    // 編輯頁第一次讀到 v5，之後讀到 v7（其他人儲存過）
    let reads = 0;
    const base = api.fetchMock.getMockImplementation()!;
    api.fetchMock.mockImplementation(async (input, init) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/profiles/standard" && (init?.method ?? "GET") === "GET") {
        reads++;
        if (reads > 1)
          return new Response(JSON.stringify({ ...standard, version: latest }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          });
      }
      return base(input, init);
    });
    await openStandard();
    const correct = within(screen.getByRole("heading", { name: /校正/ }).closest("li")!);
    fireEvent.change(correct.getByLabelText("模型"), { target: { value: "m2" } });
    await userEvent.click(screen.getByRole("button", { name: "儲存變更" }));
    const dialog = await screen.findByRole("dialog", { name: "處理方案已被其他人修改" });
    expect(within(dialog).getByText(/v5，現在已經是 v7/)).toBeTruthy();
    expect(api.calls("PATCH", "/api/profiles/standard")).toHaveLength(0);
    await userEvent.click(within(dialog).getByRole("button", { name: "仍要儲存" }));
    // 回應的版本不是開始時 + 1：明確告知
    expect(await screen.findByText(/同時有其他修改寫入；目前是 v8/)).toBeTruthy();
    expect(api.calls("PATCH", "/api/profiles/standard")).toHaveLength(1);
  });

  it("422 的欄位錯誤顯示在對應欄位旁", async () => {
    profilesApi({
      "PATCH /api/profiles/{id}": () =>
        apiError(422, "invalid_request", "設定不正確", {
          errors: [{ path: "stages.correct.provider", message: "找不到 LLM provider「nope」。" }],
        }),
    });
    await openStandard();
    const correct = within(screen.getByRole("heading", { name: /校正/ }).closest("li")!);
    fireEvent.change(correct.getByLabelText("Provider"), { target: { value: "nope" } });
    await userEvent.click(screen.getByRole("button", { name: "儲存變更" }));
    expect(await screen.findByText("找不到 LLM provider「nope」。")).toBeTruthy();
    expect(screen.getByText(/有 1 個欄位不正確/)).toBeTruthy();
    expect(correct.getByLabelText("Provider").getAttribute("aria-invalid")).toBe("true");
  });

  it("關鍵畫面與筆記：沿用時沒有子欄位，選了執行方式才寫進處理方案（axe 無違規）", async () => {
    const api = profilesApi({
      "GET /api/note-templates": () => ({
        templates: [
          { id: "video-notes", name: "Video Notes 預設", version: 1, builtin: true },
          { id: "mine", name: "mine", version: 2, builtin: false },
        ],
      }),
      "PATCH /api/profiles/{id}": () => ({ ...standard, version: 6 }),
    });
    const { container } = await openStandard();
    const flow = screen.getByRole("list", { name: "處理流程" });
    expect(within(flow).getByText("關鍵畫面")).toBeTruthy();
    expect(within(flow).getByText("筆記")).toBeTruthy();

    const frames = within(screen.getByRole("heading", { name: /關鍵畫面/ }).closest("li")!);
    const framesExec = frames.getByLabelText("執行方式") as HTMLSelectElement;
    // frames 不能交給 Agent（FRM F6）；沿用時沒有張數欄位
    expect([...framesExec.options].map((o) => o.value)).toEqual(["", "rules", "api", "off"]);
    expect(frames.queryByLabelText("每分鐘最多幾張")).toBeNull();
    await userEvent.selectOptions(framesExec, "rules");
    expect(frames.queryByLabelText("Provider")).toBeNull();
    fireEvent.change(frames.getByLabelText("每分鐘最多幾張"), { target: { value: "0.5" } });
    fireEvent.change(frames.getByLabelText("整部最多幾張"), { target: { value: "12" } });

    const note = within(screen.getByRole("heading", { name: /筆記/ }).closest("li")!);
    expect(note.queryByLabelText("筆記模板")).toBeNull();
    await userEvent.selectOptions(note.getByLabelText("執行方式"), "api");
    fireEvent.change(note.getByLabelText("Provider"), { target: { value: "gemini" } });
    fireEvent.change(note.getByLabelText("模型"), { target: { value: "g1" } });
    await waitFor(() =>
      expect(note.getByRole("option", { name: "Video Notes 預設（video-notes）" })).toBeTruthy(),
    );
    await userEvent.selectOptions(note.getByLabelText("筆記模板"), "mine");
    await userEvent.selectOptions(note.getByLabelText("筆記的寫法"), "gemini");
    await expectNoA11yViolations(container);

    await userEvent.click(screen.getByRole("button", { name: "儲存變更" }));
    expect(await screen.findByText("已儲存（v6）")).toBeTruthy();
    const body = JSON.parse(String(api.calls("PATCH", "/api/profiles/standard")[0]!.init.body));
    expect(body.config.stages).toEqual({
      frames: { executor: "rules", per_minute: 0.5, max_frames: 12 },
      note: {
        executor: "api",
        provider: "gemini",
        model: "g1",
        template: "mine",
        engine: "gemini",
      },
    });
  });

  it("筆記步驟改回沿用全域設定時，連同模板整段刪掉", async () => {
    const withNote = profile("standard", {
      version: 5,
      config: { stages: { note: { executor: "agent", template: "mine@2" } } },
    });
    const api = profilesApi({
      "GET /api/profiles/{id}": () => withNote,
      "GET /api/note-templates": () => ({ templates: [] }),
      "PATCH /api/profiles/{id}": () => ({ ...withNote, version: 6 }),
    });
    await openStandard();
    const note = within(screen.getByRole("heading", { name: /筆記/ }).closest("li")!);
    // 固定版本或清單沒有的模板原樣列出
    expect((note.getByLabelText("筆記模板") as HTMLSelectElement).value).toBe("mine@2");
    await userEvent.selectOptions(note.getByLabelText("執行方式"), "");
    expect(note.queryByLabelText("筆記模板")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "儲存變更" }));
    await screen.findByText("已儲存（v6）");
    const body = JSON.parse(String(api.calls("PATCH", "/api/profiles/standard")[0]!.init.body));
    // stages 只有 note：整個 stages 變空，一起刪掉
    expect(body).toEqual({ config: { stages: null } });
  });

  it("筆記步驟的 422 顯示在筆記的欄位旁", async () => {
    profilesApi({
      "PATCH /api/profiles/{id}": () =>
        apiError(422, "invalid_request", "設定不正確", {
          errors: [
            {
              path: "stages.note.provider",
              message: "engine: gemini 需要 type 為 gemini 的 provider。",
            },
          ],
        }),
    });
    await openStandard();
    const note = within(screen.getByRole("heading", { name: /筆記/ }).closest("li")!);
    await userEvent.selectOptions(note.getByLabelText("執行方式"), "api");
    fireEvent.change(note.getByLabelText("Provider"), { target: { value: "anthropic" } });
    await userEvent.click(screen.getByRole("button", { name: "儲存變更" }));
    expect(
      await screen.findByText("engine: gemini 需要 type 為 gemini 的 provider。"),
    ).toBeTruthy();
    expect(note.getByLabelText("Provider").getAttribute("aria-invalid")).toBe("true");
  });

  it("每個輸出可以選檔案被改過時的處理方式（OUT-R12、VAULT-R6）", async () => {
    const api = profilesApi({
      "PATCH /api/profiles/{id}": () => ({ ...standard, version: 6 }),
    });
    await openStandard();
    const card = within(screen.getByRole("group", { name: "輸出 1" }));
    const sel = card.getByLabelText("檔案被改過時") as HTMLSelectElement;
    expect(sel.value).toBe("");
    await userEvent.selectOptions(sel, "skip");
    // 換目標時保留（三種目標都有這個欄位）
    await userEvent.selectOptions(card.getByLabelText("輸出目標"), "folder");
    expect((card.getByLabelText("檔案被改過時") as HTMLSelectElement).value).toBe("skip");
    await userEvent.selectOptions(card.getByLabelText("輸出目標"), "sidecar");
    await userEvent.click(screen.getByRole("button", { name: "儲存變更" }));
    expect(await screen.findByText("已儲存（v6）")).toBeTruthy();
    const body = JSON.parse(String(api.calls("PATCH", "/api/profiles/standard")[0]!.init.body));
    expect(body.config.outputs).toEqual([
      { target: "sidecar", formats: ["srt"], on_user_change: "skip" },
    ]);
  });

  it("字幕來源的後備：沿用、不用後備（空陣列）、自訂順序", async () => {
    const api = profilesApi({
      "PATCH /api/profiles/{id}": () => ({ ...standard, version: 6 }),
    });
    await openStandard();
    const source = within(screen.getByRole("heading", { name: /字幕來源/ }).closest("li")!);
    const mode = source.getByLabelText("平台字幕都不符合時改用") as HTMLSelectElement;
    expect(mode.value).toBe("");
    await userEvent.selectOptions(mode, "none");
    expect(source.queryByLabelText("依序改用的來源")).toBeNull();
    await userEvent.selectOptions(mode, "custom");
    await userEvent.type(source.getByLabelText("依序改用的來源"), "groq, supadata");
    await userEvent.click(screen.getByRole("button", { name: "儲存變更" }));
    expect(await screen.findByText("已儲存（v6）")).toBeTruthy();
    const body = JSON.parse(String(api.calls("PATCH", "/api/profiles/standard")[0]!.init.body));
    expect(body.config.language).toEqual({ fallback: ["groq", "supadata"] });
  });

  it("字幕來源的後備選「不用後備」送空陣列", async () => {
    const api = profilesApi({
      "PATCH /api/profiles/{id}": () => ({ ...standard, version: 6 }),
    });
    await openStandard();
    const source = within(screen.getByRole("heading", { name: /字幕來源/ }).closest("li")!);
    await userEvent.selectOptions(source.getByLabelText("平台字幕都不符合時改用"), "none");
    await userEvent.click(screen.getByRole("button", { name: "儲存變更" }));
    expect(await screen.findByText("已儲存（v6）")).toBeTruthy();
    const body = JSON.parse(String(api.calls("PATCH", "/api/profiles/standard")[0]!.init.body));
    expect(body.config.language).toEqual({ fallback: [] });
  });

  it("設為預設", async () => {
    const api = profilesApi({
      "POST /api/profiles/{id}/default": () => ({ ...standard, default: true }),
    });
    await openStandard();
    await userEvent.click(screen.getByRole("button", { name: "設為預設" }));
    expect(await screen.findByText("已設為預設")).toBeTruthy();
    expect(api.calls("POST", "/api/profiles/standard/default")).toHaveLength(1);
  });
});
