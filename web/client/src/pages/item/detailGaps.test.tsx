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
