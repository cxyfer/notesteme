// 時間帶的其他圖層（README 10.30）：說話密度、畫面刻度、處理範圍（chunk 的時間範圍）與「重新校正這段」。
import { screen, waitFor } from "@testing-library/react";
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
