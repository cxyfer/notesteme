// pages.md §4 的缺口（README 10.25）：匯出 ASS 選擇樣式。
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes } from "../../test/fixtures";
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
