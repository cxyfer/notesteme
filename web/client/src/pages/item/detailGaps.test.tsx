// pages.md §4 的缺口（README 10.26）：匯出 ASS 選擇樣式、章節、時間帶的拖曳選取與複製。
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { cue, defaultRoutes, detail, item, stage } from "../../test/fixtures";
import { apiError, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { style } from "../../test/tuneFixtures";

function open(path: string, routes: Record<string, MockHandler> = {}) {
  const api = mockApi({ ...defaultRoutes(), ...routes });
  signIn();
  return { api, ...renderRoute(path) };
}

describe("影音詳情：匯出 ASS 時選擇樣式（OUT-R6）", () => {
  it("樣式選單改變下載連結的 style；axe 無違規", async () => {
    const { container } = open("/items/item-1?tab=outputs", {
      "GET /api/ass-styles": () => ({
        styles: [
          style("default", { builtin: true }),
          style("large", { builtin: true }),
          style("mine"),
        ],
      }),
    });
    const link = await screen.findByRole("link", { name: "ASS（default）" });
    expect(link.getAttribute("href")).toBe("/api/items/item-1/subtitles?format=ass&style=default");
    await userEvent.selectOptions(screen.getByLabelText("ASS 樣式"), "mine");
    expect(screen.getByRole("link", { name: "ASS（mine）" }).getAttribute("href")).toBe(
      "/api/items/item-1/subtitles?format=ass&style=mine",
    );
    // 其他格式不受影響
    expect(screen.getByRole("link", { name: "WebVTT" }).getAttribute("href")).toBe(
      "/api/items/item-1/subtitles?format=vtt",
    );
    await expectNoA11yViolations(container);
  });

  it("樣式清單讀不到時退回預設樣式的連結，不擋下載", async () => {
    open("/items/item-1?tab=outputs", {
      "GET /api/ass-styles": () => apiError(403, "forbidden", "權限不足。"),
    });
    const link = await screen.findByRole("link", { name: "ASS（預設樣式）" });
    expect(link.getAttribute("href")).toBe("/api/items/item-1/subtitles?format=ass");
    expect(screen.queryByLabelText("ASS 樣式")).toBeNull();
  });
});

const CHAPTERS = [
  { title: "開場", start: "00:00:00.000", end: "00:01:00.000", origin: "llm" as const },
  {
    title: "第二章：<b>不是標籤</b>",
    start: "00:01:00.000",
    end: "00:05:00.000",
    origin: "llm" as const,
  },
  { title: "結語", start: "00:05:00.000", end: "00:10:00.000", origin: "llm" as const },
];

function openWithChapters(chapters: unknown = CHAPTERS) {
  return open("/items/item-1", {
    "GET /api/items/{id}": () => ({
      ...detail(item(1, { platform: "local", duration_s: 600, original_lang: "zh-Hans" }), [
        stage("metadata", "done"),
        stage("transcript", "done", { executor: "local" }),
      ]),
      chapters,
    }),
    "GET /api/items/{id}/cues": () => ({
      variant: "source",
      lang: "zh-Hans",
      cues: [
        cue(1, 0, 2, "第一句"),
        cue(2, 58, 62, "跨過一分鐘的句子"),
        cue(3, 90, 95, "第二章的句子"),
        cue(4, 400, 402, "結語的句子"),
      ],
    }),
  });
}

function sliderOf() {
  const slider = screen.getByRole("slider");
  slider.getBoundingClientRect = () =>
    ({ left: 0, width: 600, top: 0, height: 100, right: 600, bottom: 100 }) as DOMRect;
  return slider;
}

describe("影音詳情：章節（CHAP-R7）", () => {
  it("播放器下列出章節，點擊跳播並標出目前的章節；標題是純文字；axe 無違規", async () => {
    const { container } = openWithChapters();
    const list = await screen.findByRole("region", { name: "章節" });
    const buttons = within(list).getAllByRole("button");
    expect(buttons.map((b) => b.getAttribute("aria-label"))).toEqual([
      "跳到章節「開場」（00:00）",
      "跳到章節「第二章：<b>不是標籤</b>」（01:00）",
      "跳到章節「結語」（05:00）",
    ]);
    expect(buttons[0]!.getAttribute("aria-current")).toBe("true");
    expect(container.querySelector("b")).toBeNull();
    const video = container.querySelector("video")!;
    await userEvent.click(buttons[2]!);
    expect(video.currentTime).toBe(300);
    fireEvent.timeUpdate(video);
    await waitFor(() => expect(buttons[2]!.getAttribute("aria-current")).toBe("true"));
    expect(buttons[0]!.getAttribute("aria-current")).toBeNull();
    await expectNoA11yViolations(container);
  });

  it("時間帶畫出章節線（第一章在 0 秒不畫），hover 顯示章節名稱", async () => {
    const { container } = openWithChapters();
    await screen.findByRole("region", { name: "章節" });
    const lines = [...container.querySelectorAll<HTMLElement>("[data-testid='chapter-line']")];
    expect(lines.map((l) => l.style.left)).toEqual(["10%", "50%"]);
    // 自己畫的 track 仍保留點狀基線
    expect(container.querySelector(".sband__track .sband__baseline")).toBeTruthy();
    fireEvent.pointerMove(sliderOf(), { clientX: 92 });
    expect(container.querySelector(".sband__hover-chapter")?.textContent).toBe(
      "第二章：<b>不是標籤</b>",
    );
    await waitFor(() =>
      expect(container.querySelector(".sband__hover-text")?.textContent).toBe("第二章的句子"),
    );
  });

  it("Shift + → 在時間帶上跳到下一章", async () => {
    const { container } = openWithChapters();
    await screen.findByRole("region", { name: "章節" });
    const slider = sliderOf();
    slider.focus();
    await userEvent.keyboard("{Shift>}{ArrowRight}{/Shift}");
    expect(container.querySelector("video")!.currentTime).toBe(60);
  });

  it("沒有章節時不顯示清單也不畫線", async () => {
    const { container } = openWithChapters([]);
    await screen.findByRole("heading", { level: 1, name: "影片 1" });
    expect(screen.queryByRole("region", { name: "章節" })).toBeNull();
    expect(container.querySelector("[data-testid='chapter-line']")).toBeNull();
  });
});

describe("影音詳情：時間帶拖曳選取後複製這段字幕", () => {
  it("複製和範圍重疊的句子（每句一行）；取消選取後動作列消失", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    const { container } = openWithChapters();
    await screen.findByRole("region", { name: "章節" });
    await screen.findByText("第一句");
    const slider = sliderOf();
    fireEvent.pointerDown(slider, { clientX: 60, button: 0, pointerId: 1, isPrimary: true });
    fireEvent.pointerMove(slider, { clientX: 100, pointerId: 1 });
    fireEvent.pointerUp(slider, { clientX: 100, pointerId: 1 });
    fireEvent.click(slider, { clientX: 100 });
    const bar = screen.getByRole("group", { name: "時間帶的選取範圍" });
    expect(within(bar).getByText("已選取 01:00–01:40")).toBeTruthy();
    // 選取不移動播放頭
    expect(container.querySelector("video")!.currentTime).toBe(0);
    await userEvent.click(within(bar).getByRole("button", { name: "複製這段字幕（2 句）" }));
    expect(writeText).toHaveBeenCalledWith("跨過一分鐘的句子\n第二章的句子");
    expect(await screen.findByText("已複製 2 句字幕。")).toBeTruthy();
    await expectNoA11yViolations(container);
    await userEvent.click(within(bar).getByRole("button", { name: "取消選取" }));
    expect(screen.queryByRole("group", { name: "時間帶的選取範圍" })).toBeNull();
    vi.unstubAllGlobals();
  });

  it("剪貼簿不能用時提示，不丟錯", async () => {
    vi.stubGlobal("navigator", { ...navigator, clipboard: undefined });
    openWithChapters();
    await screen.findByText("第一句");
    const slider = sliderOf();
    fireEvent.pointerDown(slider, { clientX: 0, button: 0, pointerId: 1, isPrimary: true });
    fireEvent.pointerMove(slider, { clientX: 30, pointerId: 1 });
    fireEvent.pointerUp(slider, { clientX: 30, pointerId: 1 });
    await userEvent.click(screen.getByRole("button", { name: "複製這段字幕（1 句）" }));
    expect(await screen.findByText("無法複製：瀏覽器不允許存取剪貼簿。")).toBeTruthy();
    vi.unstubAllGlobals();
  });
});

describe("影音詳情：紀錄分頁的產出版本與比較（LIB-R11、LIB-R25）", () => {
  const version = (id: string, over: Record<string, unknown> = {}) => ({
    id,
    kind: "transcript",
    variant: "corrected",
    lang: "en",
    format: "notesteme-cues+json",
    size: 10,
    created_at: "2026-11-20T00:00:00Z",
    current: false,
    stage: "correct",
    executor: "api",
    ...over,
  });

  function openVersions(extra: Record<string, MockHandler> = {}) {
    return open("/items/item-1?tab=runs", {
      "GET /api/items/{id}/artifacts": () => ({
        artifacts: [
          version("v3", { current: true, provider: "openai", created_at: "2026-11-22T00:00:00Z" }),
          version("v2", { provider: "anthropic", created_at: "2026-11-21T00:00:00Z" }),
          version("n1", { kind: "note", variant: undefined, current: true, format: "md" }),
          version("c1", { kind: "chapters", variant: undefined, current: true }),
          version("c0", { kind: "chapters", variant: undefined }),
        ],
      }),
      "GET /api/artifacts/{id}/content": (req) =>
        req.params.id === "v2"
          ? {
              variant: "corrected",
              lang: "en",
              cues: [cue(1, 0, 2, "PostgreSQL is a database."), cue(2, 2, 4, "Same line.")],
            }
          : {
              variant: "corrected",
              lang: "en",
              cues: [cue(1, 0, 2, "PostgreSQL is a database system."), cue(2, 2, 4, "Same line.")],
            },
      ...extra,
    });
  }

  it("依種類分組列出版本；字幕選兩個版本比較逐字差異；只列版本的種類沒有比較；axe 無違規", async () => {
    const { container, api } = openVersions();
    const section = await screen.findByRole("region", { name: "產出的版本" });
    expect(await within(section).findByRole("heading", { name: /字幕，校正，en/ })).toBeTruthy();
    // 字幕（校正）與章節各有 2 個版本
    expect(within(section).getAllByText("（2 個版本）", { exact: false })).toHaveLength(2);
    // 章節只列版本、不能比較；筆記只有一版也不能比較
    expect(within(section).getAllByRole("button", { name: /比較「/ })).toHaveLength(1);
    await userEvent.click(
      within(section).getByRole("button", { name: "比較「字幕，校正，en」的兩個版本" }),
    );
    expect(await within(section).findByText("有 1 處不同。")).toBeTruthy();
    const ins = container.querySelector(".versions__diff ins")!;
    expect(ins.textContent).toBe("新增： system");
    expect(api.calls("GET", "/api/artifacts/v2/content")).toHaveLength(1);
    expect(api.calls("GET", "/api/artifacts/v3/content")).toHaveLength(1);
    await expectNoA11yViolations(container);
  });

  it("筆記：用 format=md 讀取兩個版本，逐行比較", async () => {
    const md = (body: string) =>
      new Response(body, { status: 200, headers: { "Content-Type": "text/markdown" } });
    const { container, api } = open("/items/item-1?tab=runs", {
      "GET /api/items/{id}/artifacts": () => ({
        artifacts: [
          version("n2", { kind: "note", variant: undefined, current: true }),
          version("n1", { kind: "note", variant: undefined }),
        ],
      }),
      "GET /api/artifacts/{id}/content": (req) =>
        req.params.id === "n1" ? md("# Talk\n\n舊的段落\n") : md("# Talk\n\n新的段落\n"),
    });
    const section = await screen.findByRole("region", { name: "產出的版本" });
    await userEvent.click(
      await within(section).findByRole("button", { name: "比較「筆記，en」的兩個版本" }),
    );
    expect(await within(section).findByText("有 1 處不同。")).toBeTruthy();
    expect(container.querySelector(".versions__diff del")!.textContent).toBe("刪除：舊");
    expect(container.querySelector(".versions__diff ins")!.textContent).toBe("新增：新");
    // 筆記沒有時間碼：仍保留空的第一欄
    expect(container.querySelector(".versions__changes li > .versions__at")!.textContent).toBe("");
    const call = api.calls("GET", "/api/artifacts/n1/content")[0]!;
    expect(call.url.searchParams.get("format")).toBe("md");
  });

  it("選同一個版本時不能比較", async () => {
    openVersions();
    const section = await screen.findByRole("region", { name: "產出的版本" });
    await within(section).findByRole("heading", { name: /字幕，校正，en/ });
    const [before] = within(section).getAllByLabelText("比較前");
    await userEvent.selectOptions(before!, "v3");
    expect(
      within(section).getByRole("button", { name: "比較「字幕，校正，en」的兩個版本" }),
    ).toHaveProperty("disabled", true);
    expect(within(section).getByText("選了同一個版本。")).toBeTruthy();
  });

  it("讀不到版本時顯示錯誤與重試", async () => {
    open("/items/item-1?tab=runs", {
      "GET /api/items/{id}/artifacts": () => apiError(404, "in_trash", "這部影音在垃圾桶中。"),
    });
    const section = await screen.findByRole("region", { name: "產出的版本" });
    expect(await within(section).findByRole("button", { name: "重試" })).toBeTruthy();
  });
});

describe("筆記的逐行差異", () => {
  it("只列出變動的地方，連續的刪除與新增合成一處", async () => {
    const { diffLines } = await import("./Versions");
    const rows = diffLines(
      "# 標題\n第一段\n第二段\n結尾",
      "# 標題\n第一段（改）\n第二段\n結尾\n新增的行",
    );
    expect(rows).toHaveLength(2);
    expect(rows[0]!.parts).toEqual([
      { type: "eq", text: "第一段" },
      { type: "ins", text: "（改）" },
    ]);
    expect(rows[1]!.parts).toEqual([{ type: "ins", text: "新增的行" }]);
    expect(diffLines("a\nb", "a\nb")).toEqual([]);
  });
});
