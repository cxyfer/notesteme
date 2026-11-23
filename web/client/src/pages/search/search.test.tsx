import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes } from "../../test/fixtures";
import { apiError, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { searchResult } from "../../test/ui5Fixtures";
import {
  groupByItem,
  jumpSecond,
  parseClock,
  queryProblem,
  splitHighlights,
  validateSearchParams,
} from "./model";

describe("搜尋：純函式", () => {
  it("依 UTF-16 位移切出命中範圍；防禦越界、重疊與亂序", () => {
    expect(splitHighlights("abcdef", [{ start: 1, end: 3 }])).toEqual([
      { text: "a", hit: false },
      { text: "bc", hit: true },
      { text: "def", hit: false },
    ]);
    // emoji 佔兩個 UTF-16 code unit
    expect(splitHighlights("😀假装", [{ start: 2, end: 4 }])).toEqual([
      { text: "😀", hit: false },
      { text: "假装", hit: true },
    ]);
    expect(
      splitHighlights("abcdef", [
        { start: 4, end: 99 },
        { start: 0, end: 2 },
        { start: 1, end: 3 },
        { start: -5, end: -1 },
      ]),
    ).toEqual([
      { text: "ab", hit: true },
      { text: "cd", hit: false },
      { text: "ef", hit: true },
    ]);
    expect(splitHighlights("abc", null)).toEqual([{ text: "abc", hit: false }]);
    expect(splitHighlights("", [])).toEqual([{ text: "", hit: false }]);
  });

  it("查詢以 code point 計算長度；URL 參數只留 q 與合法的 lang", () => {
    expect(queryProblem("  ")).toBe("empty");
    expect(queryProblem("😀".repeat(200))).toBeUndefined();
    expect(queryProblem("a".repeat(201))).toBe("tooLong");
    expect(validateSearchParams({ q: " log ", lang: "zh-Hant", x: 1 })).toEqual({
      q: "log",
      lang: "zh-Hant",
    });
    expect(validateSearchParams({ q: 42, lang: "<script>" })).toEqual({ q: "42" });
  });

  it("跳轉時間取 ceil(start)，超過這一句時退回 floor", () => {
    expect(parseClock("01:02:03.5")).toBe(3723.5);
    expect(parseClock("x")).toBeUndefined();
    expect(jumpSecond("00:00:04.000", "00:00:08.000")).toBe(4);
    expect(jumpSecond("00:00:04.300", "00:00:08.000")).toBe(5);
    expect(jumpSecond("00:00:04.300", "00:00:04.800")).toBe(4);
  });

  it("連續同一部影音的結果分成一組", () => {
    const g = groupByItem([searchResult(1), searchResult(1, { cue: 3 }), searchResult(2)]);
    expect(g.map((x) => x.length)).toEqual([2, 1]);
  });
});

function searchApi(handler: MockHandler) {
  return mockApi({ ...defaultRoutes(), "GET /api/search": handler });
}

describe("搜尋頁", () => {
  it("命中範圍是 <mark>，字幕的 HTML 不會被渲染；點擊打開影音詳情的那個時間（axe 無違規）", async () => {
    const api = searchApi(() => ({
      results: [
        searchResult(1),
        searchResult(1, {
          cue: 9,
          start: "00:01:00.000",
          end: "00:01:02.000",
          text: "<b>ordered log</b>",
          highlights: [{ start: 3, end: 14 }],
        }),
      ],
      total: 2,
    }));
    signIn();
    const { container, router } = renderRoute("/search?q=ordered%20log&lang=en");
    expect(await screen.findByText("2 句符合")).toBeTruthy();
    expect(api.calls("GET", "/api/search")[0]!.url.search).toBe("?q=ordered+log&lang=en&limit=50");
    const marks = [...container.querySelectorAll("mark")].map((m) => m.textContent);
    expect(marks).toEqual(["ordered log", "ordered log"]);
    expect(container.querySelector("b")).toBeNull();
    expect(screen.getByText(/<b>/)).toBeTruthy();
    expect(screen.getByRole("heading", { level: 2, name: "影片 1" })).toBeTruthy();
    await expectNoA11yViolations(container);

    await userEvent.setup().click(screen.getByRole("link", { name: /01:00/ }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/items/item-1"));
    expect(router.state.location.search).toEqual({ t: 60 });
  });

  it("送出前檢查空白與長度；送出後查詢寫進 URL", async () => {
    const api = searchApi(() => ({ results: [], total: 0 }));
    signIn();
    const { router } = renderRoute("/search");
    const user = userEvent.setup();
    const input = await screen.findByLabelText("要搜尋的文字");
    await user.click(screen.getByRole("button", { name: "搜尋" }));
    expect(screen.getByText("請輸入要搜尋的文字。")).toBeTruthy();
    expect(api.calls("GET", "/api/search")).toHaveLength(0);
    await user.type(input, "假装");
    await user.click(screen.getByRole("button", { name: "搜尋" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ q: "假装" }));
    expect(await screen.findByText("沒有字幕含有「假装」。")).toBeTruthy();
  });

  it("伺服器的 422 顯示訊息，不提供重試", async () => {
    searchApi(() => apiError(422, "invalid_request", "搜尋文字含有無法使用的字元。"));
    signIn();
    renderRoute("/search?q=bad");
    expect(await screen.findByText("搜尋文字含有無法使用的字元。")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "重試" })).toBeNull();
  });

  it("游標分頁：載入更多帶上 next_cursor", async () => {
    const api = searchApi((req) =>
      req.url.searchParams.get("cursor")
        ? { results: [searchResult(2)], total: 2 }
        : { results: [searchResult(1)], total: 2, next_cursor: "c2" },
    );
    signIn();
    renderRoute("/search?q=log");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "載入更多" }));
    expect(await screen.findByRole("heading", { level: 2, name: "影片 2" })).toBeTruthy();
    expect(api.calls("GET", "/api/search")[1]!.url.searchParams.get("cursor")).toBe("c2");
  });

  it("媒體庫的搜尋欄可以改搜字幕內容", async () => {
    mockApi(defaultRoutes());
    signIn();
    const { router } = renderRoute("/library?q=log");
    await userEvent
      .setup()
      .click(await screen.findByRole("link", { name: "在字幕內容中搜尋「log」" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/search"));
    expect(router.state.location.search).toEqual({ q: "log" });
  });
});
