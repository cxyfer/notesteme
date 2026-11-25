// 時間帶的其他圖層（README 10.30）：說話密度、畫面刻度、處理範圍（chunk 的時間範圍）與「重新校正這段」。
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { cue, defaultRoutes, detail, item, stage } from "../../test/fixtures";
import { mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import type { Cue } from "./cues";
import { densityPaths, densitySummary, speechDensity } from "./density";

function open(path: string, routes: Record<string, MockHandler> = {}) {
  const api = mockApi({ ...defaultRoutes(), ...routes });
  signIn();
  return { api, ...renderRoute(path) };
}

const c = (start: number, end: number, text: string): Cue => ({ index: 0, start, end, text });

describe("說話密度的計算", () => {
  it("依重疊時間加權：字數平均分到句子的時間範圍，空白不算字", () => {
    // 10 秒、10 個區間；一句 0–4 秒 8 個字（每秒 2 字），一句 5–6 秒 3 個字（每秒 3 字）
    const v = speechDensity([c(0, 4, "一二三四 五六七八"), c(5, 6, "a b c")], 10, 10);
    expect(v).toHaveLength(10);
    // 平滑前：[2,2,2,2,0,3,0,0,0,0]；1-2-1 平滑後
    expect(v[0]).toBeCloseTo(2);
    expect(v[3]).toBeCloseTo((2 + 4 + 0) / 4);
    expect(v[5]).toBeCloseTo((0 + 6 + 0) / 4);
    expect(v[9]).toBe(0);
  });

  it("沒有長度或沒有字幕時是空的，也沒有輪廓", () => {
    expect(speechDensity([], 10)).toEqual([]);
    expect(speechDensity([c(0, 1, "a")], 0)).toEqual([]);
    expect(densityPaths([])).toBeNull();
    expect(densityPaths([0, 0])).toBeNull();
  });

  it("文字版本：最密集的區間與最長的沉默（短於 5 秒的停頓不算）", () => {
    const cues = [c(0, 10, "x".repeat(10)), c(12, 20, "y".repeat(80)), c(60, 100, "z".repeat(40))];
    const v = speechDensity(cues, 100, 100);
    const s = densitySummary(cues, 100, v);
    expect(s).not.toBeNull();
    // 視窗是全長的 1/20（5 秒），落在每秒 10 字的 12–20 秒內
    expect(s!.dense.from).toBeGreaterThanOrEqual(11);
    expect(s!.dense.to).toBeLessThanOrEqual(21);
    expect(s!.silence).toEqual({ from: 20, to: 60 });
    // 句子之間只有短停頓時沒有沉默
    const tight = [c(0, 50, "a".repeat(50)), c(52, 100, "b".repeat(48))];
    expect(densitySummary(tight, 100, speechDensity(tight, 100, 100))!.silence).toBeNull();
  });
});

function itemWithCues(stages = [stage("metadata", "done"), stage("transcript", "done")]) {
  return {
    "GET /api/items/{id}": () =>
      detail(item(1, { platform: "local", duration_s: 120, original_lang: "zh-Hant" }), stages),
    "GET /api/items/{id}/cues": () => ({
      variant: "source",
      lang: "zh-Hant",
      cues: [cue(1, 0, 10, "開場的一句話"), cue(2, 40, 60, "很長的一段說明，講了很多很多的內容")],
    }),
  } satisfies Record<string, MockHandler>;
}

describe("影音詳情：時間帶的說話密度", () => {
  it("有原始字幕時畫出輪廓（取代點狀基線），文字版本放在 slider 的 aria-describedby；axe 無違規", async () => {
    const { container, api } = open("/items/item-1", itemWithCues());
    const slider = await screen.findByRole("slider", { name: "時間帶：播放位置" });
    await waitFor(() => expect(container.querySelector("[data-testid=density]")).not.toBeNull());
    expect(container.querySelector(".item__band .sband__baseline")).toBeNull();
    // 密度固定用原始字幕
    expect(
      api
        .calls("GET", "/api/items/{id}/cues")
        .some((x) => x.url.searchParams.get("variant") === "source"),
    ).toBe(true);
    const descId = slider.getAttribute("aria-describedby");
    expect(descId).toBeTruthy();
    const desc = document.getElementById(descId!)!;
    expect(desc.textContent).toContain("最密集的是 00:40–00:46。");
    expect(desc.textContent).toContain("最長的沉默是 01:00–02:00。");
    await expectNoA11yViolations(container);
  });

  it("還沒有轉錄時只畫點狀基線，沒有 aria-describedby", async () => {
    const { container } = open(
      "/items/item-1",
      itemWithCues([stage("metadata", "done"), stage("transcript", "running")]),
    );
    const slider = await screen.findByRole("slider", { name: "時間帶：播放位置" });
    expect(container.querySelector(".item__band .sband__baseline")).not.toBeNull();
    expect(container.querySelector("[data-testid=density]")).toBeNull();
    expect(slider.getAttribute("aria-describedby")).toBeNull();
  });
});

describe("影音詳情：時間帶的畫面刻度", () => {
  const withFrames = () => ({
    ...itemWithCues([
      stage("metadata", "done"),
      stage("transcript", "done"),
      stage("frames", "done"),
    ]),
    "GET /api/items/{id}/frames": () => ({
      target: 1,
      range: { min: 1, max: 2 },
      candidate_count: 2,
      selected_count: 1,
      shortfall: false,
      frames: [
        {
          n: 1,
          at: "00:00:30.000",
          score: 0.9,
          selected: true,
          excluded: null,
          caption: "架構圖",
          chapter: "",
          width: 1,
          height: 1,
        },
        {
          n: 2,
          at: "00:01:30.000",
          score: 0.1,
          selected: false,
          excluded: null,
          caption: "",
          chapter: "",
          width: 1,
          height: 1,
        },
      ],
    }),
  });

  it("選中與候選的畫面畫在時間帶上（裝飾），文字版本在 aria-describedby；axe 無違規", async () => {
    const { container } = open("/items/item-1", withFrames());
    const slider = await screen.findByRole("slider", { name: "時間帶：播放位置" });
    await waitFor(() => expect(container.querySelectorAll(".sband__mark")).toHaveLength(2));
    const sel = container.querySelector<HTMLElement>(".sband__mark--selected")!;
    expect(sel.style.left).toBe("25%");
    expect(container.querySelector<HTMLElement>(".sband__mark--candidate")!.style.left).toBe("75%");
    // 刻度在 aria-hidden 的圖層內，不新增可聚焦的元素
    expect(sel.closest("[aria-hidden=true]")).not.toBeNull();
    const desc = document.getElementById(slider.getAttribute("aria-describedby")!)!;
    expect(desc.textContent).toContain("選中的 1 張畫面");
    expect(desc.textContent).toContain("其他 1 張候選");
    await expectNoA11yViolations(container);
  });

  it("點擊選中畫面附近時對齊到它的時間，hover 顯示畫面的說明", async () => {
    const { container } = open("/items/item-1", withFrames());
    const slider = await screen.findByRole("slider", { name: "時間帶：播放位置" });
    await waitFor(() => expect(container.querySelector(".sband__mark--selected")).not.toBeNull());
    // jsdom 沒有版面：寬 400px，畫面 1（30 秒）在 x=100
    slider.getBoundingClientRect = () =>
      ({ left: 0, width: 400, top: 0, height: 80, right: 400, bottom: 80, x: 0, y: 0 }) as DOMRect;
    fireEvent.pointerMove(slider, { clientX: 104 });
    expect(container.querySelector(".sband__hover-time")!.textContent).toBe("00:30");
    expect(container.querySelector(".sband__hover-mark")!.textContent).toBe("畫面 1：架構圖");
    fireEvent.click(slider, { clientX: 104 });
    expect(slider.getAttribute("aria-valuetext")).toBe("播放位置 00:30");
    // 離得遠時照點擊的位置
    fireEvent.click(slider, { clientX: 200 });
    expect(slider.getAttribute("aria-valuetext")).toBe("播放位置 01:00");
  });

  it("frames 步驟沒有完成時不讀取畫面", async () => {
    const { api } = open("/items/item-1", itemWithCues());
    await screen.findByRole("slider", { name: "時間帶：播放位置" });
    expect(api.calls("GET", "/api/items/{id}/frames")).toHaveLength(0);
  });
});

const ch = (chunk: number, status: string, start: string, end: string, over = {}) => ({
  stage: "correct",
  chunk,
  status,
  start,
  end,
  current: true,
  ...over,
});
const CHUNKS = [
  ch(1, "done", "00:00:00.000", "00:00:39.500"),
  ch(2, "running", "00:00:40.000", "00:01:19.500"),
  ch(3, "pending", "00:01:20.000", "00:01:59.500"),
];

function itemWithChunks(
  correct: string,
  chunks: unknown[],
  over: Record<string, MockHandler> = {},
) {
  return {
    ...itemWithCues([
      stage("metadata", "done"),
      stage("transcript", "done"),
      stage("segment", "done"),
      stage("correct", correct, { executor: "api" }),
    ]),
    "GET /api/items/{id}/chunks": () => ({ chunks }),
    ...over,
  } satisfies Record<string, MockHandler>;
}

describe("影音詳情：步驟帶依 chunk 的時間範圍畫出處理到哪裡（LIB-R29）", () => {
  it("處理中：已完成、處理中、未開始的 chunk 畫在各自的時間，標籤是「校正 1/3」，文字版本列出每段；axe 無違規", async () => {
    const { container } = open("/items/item-1", itemWithChunks("running", CHUNKS));
    const slider = await screen.findByRole("slider", { name: "時間帶：播放位置" });
    const band = () => container.querySelector('.sband__band[data-stage="correct"]')!;
    await waitFor(() => expect(band().querySelectorAll(".sband__seg")).toHaveLength(3));
    const segs = [...band().querySelectorAll<HTMLElement>(".sband__seg")];
    expect(segs.map((s) => s.dataset.status)).toEqual(["done", "running", "pending"]);
    expect(segs[1]!.style.left).toBe(`${(40 / 120) * 100}%`);
    // 處理中的範圍也淡淡標在底軌上
    expect(container.querySelector(".sband__range--running")).not.toBeNull();
    expect(container.querySelector('.sband__label[data-stage="correct"]')!.textContent).toBe(
      "校正 1/3",
    );
    const desc = document.getElementById(slider.getAttribute("aria-describedby")!)!;
    expect(desc.textContent).toContain(
      "校正：第 1 段完成（00:00–00:39），第 2 段處理中（00:40–01:19），第 3 段未開始（01:20–01:59）。",
    );
    await expectNoA11yViolations(container);
  });

  it("失敗：失敗的 chunk 用失敗的區段與底軌標出，標籤寫出第幾段", async () => {
    const { container } = open(
      "/items/item-1",
      itemWithChunks("failed", [
        ch(1, "done", "00:00:00.000", "00:00:39.500"),
        ch(2, "failed", "00:00:40.000", "00:01:19.500"),
      ]),
    );
    await waitFor(() => expect(container.querySelector(".sband__range--failed")).not.toBeNull());
    expect(container.querySelector('.sband__label[data-stage="correct"]')!.textContent).toBe(
      "校正：失敗：第 2 段",
    );
  });

  it("不是這次執行的切分（current: false）與已完成的步驟不畫 chunk", async () => {
    const { container } = open(
      "/items/item-1",
      itemWithChunks("running", [
        ch(1, "done", "00:00:00.000", "00:01:00.000", { current: false }),
      ]),
    );
    await screen.findByRole("slider", { name: "時間帶：播放位置" });
    await waitFor(() =>
      expect(container.querySelector('.sband__label[data-stage="correct"]')!.textContent).toBe(
        "校正：處理中",
      ),
    );
    expect(
      container.querySelectorAll('.sband__band[data-stage="correct"] .sband__seg'),
    ).toHaveLength(1);
  });
});
