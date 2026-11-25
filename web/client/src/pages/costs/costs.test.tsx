import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { costEntry, costSummary, priceList } from "../../test/m4Fixtures";
import { mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { daysInclusive, validateCostsSearch } from "./search";

describe("成本：search params", () => {
  it("丟掉不合法的值；day 是預設不寫進 URL", () => {
    expect(validateCostsSearch({ group_by: "day", from: "2026-11-01", to: "x" })).toEqual({
      from: "2026-11-01",
    });
    expect(validateCostsSearch({ group_by: "provider" })).toEqual({ group_by: "provider" });
    expect(validateCostsSearch({ group_by: "nope" })).toEqual({});
    expect(daysInclusive("2026-11-01", "2026-11-01")).toBe(1);
    expect(daysInclusive("2025-11-01", "2026-11-02")).toBe(367);
  });
});

function costsApi(extra: Record<string, MockHandler> = {}) {
  return mockApi({
    "GET /api/costs": () => costSummary(),
    "GET /api/costs/entries": (req) =>
      req.url.searchParams.get("cursor")
        ? { entries: [costEntry(3)] }
        : {
            entries: [
              costEntry(1),
              costEntry(2, { state: "unknown", usd: 0, item_title: "沒有單價的影片" }),
            ],
            next_cursor: "c2",
          },
    "GET /api/prices": () => priceList(),
    ...extra,
  });
}

describe("成本頁", () => {
  it("頂部一行用 2 位小數；未知單價、預留與長條圖的文字（axe 無違規）", async () => {
    costsApi();
    signIn();
    const { container } = renderRoute("/costs");
    await screen.findByRole("heading", { level: 1, name: "成本" });
    const headline = await screen.findByText(/本月花費/);
    expect(headline.textContent).toMatch(
      /本月花費 US\$3\.42，預算 US\$20\.00，預計月底約 US\$9\.80。/,
    );
    expect(screen.getByText(/另有 US\$0\.12 預留/)).toBeTruthy();
    expect(screen.getByText(/有 2 次呼叫找不到單價/)).toBeTruthy();
    // 未知單價不顯示 $0.00
    const row = (await screen.findByText("沒有單價的影片")).closest("tr")!;
    expect(within(row).getByText("未知單價")).toBeTruthy();
    expect(within(row).queryByText(/\$0\.00/)).toBeNull();
    // 長條圖的資料也以文字呈現
    const chart = screen.getByRole("table", { name: "依日期的花費" });
    expect(within(chart).getByText("2026-11-22")).toBeTruthy();
    expect(within(chart).getByText(/預留 US\$0\.12/)).toBeTruthy();
    await expectNoA11yViolations(container);
  });

  it("寬表格在可以用鍵盤聚焦的捲動區裡（375px 會水平捲動；scrollable-region-focusable）", async () => {
    costsApi();
    signIn();
    renderRoute("/costs");
    const chart = await screen.findByRole("table", { name: "依日期的花費" });
    await screen.findByText("沒有單價的影片");
    for (const table of screen.getAllByRole("table")) {
      const region = table.closest('[role="region"]');
      expect(region, `${table.getAttribute("aria-label") ?? "表格"} 不在捲動區裡`).not.toBeNull();
      expect(region!.getAttribute("tabindex")).toBe("0");
      expect(region!.getAttribute("aria-label")).toBeTruthy();
    }
    expect(chart.closest('[role="region"]')!.getAttribute("aria-label")).toBe(
      "依日期的花費（可捲動的表格）",
    );
    // 捲動區和所在的 <section> 不同名（landmark-unique）
    const names = screen
      .getAllByRole("region")
      .map(
        (r) =>
          r.getAttribute("aria-label") ??
          document.getElementById(r.getAttribute("aria-labelledby") ?? "")?.textContent ??
          "",
      );
    expect(new Set(names).size, names.join(" / ")).toBe(names.length);
  });

  it("沒有月預算時改用另一句；prices_stale 依 code 提供「確認計價表」", async () => {
    const api = costsApi({
      "GET /api/costs": () =>
        costSummary({
          budget: {
            month: "2026-11",
            monthly_usd: null,
            spent_usd: 1,
            reserved_usd: 0,
            projected_usd: 2,
          },
          unknown_calls: 0,
          notices: [
            {
              code: "prices_stale",
              message: "計價表已經 120 天沒有確認，估價可能不準確。",
              actions: ["確認計價表", "調整單價"],
            },
          ],
        }),
      "POST /api/prices/confirm": () => priceList(),
    });
    signIn();
    const user = userEvent.setup();
    renderRoute("/costs");
    expect(await screen.findByText(/沒有設定月預算/)).toBeTruthy();
    const notice = screen
      .getByText("計價表已經 120 天沒有確認，估價可能不準確。")
      .closest(".callout")!;
    await user.click(within(notice as HTMLElement).getByRole("button", { name: "確認計價表" }));
    await waitFor(() => expect(api.calls("POST", "/api/prices/confirm")).toHaveLength(1));
  });

  it("分組與日期在 URL；超過 366 天不能套用也不能匯出", async () => {
    const api = costsApi();
    signIn();
    const user = userEvent.setup();
    const { router } = renderRoute("/costs?group_by=provider");
    await screen.findByText(/本月花費/);
    expect(api.calls("GET", "/api/costs")[0]!.url.searchParams.get("group_by")).toBe("provider");
    const exportLink = screen.getByRole("link", { name: "匯出 CSV" });
    expect(exportLink.getAttribute("href")).toBe(
      "/api/costs/export.csv?from=2026-11-01&to=2026-11-23",
    );
    await user.selectOptions(screen.getByLabelText("分組"), "stage");
    await waitFor(() => expect(router.state.location.search).toMatchObject({ group_by: "stage" }));
    fireEvent.change(screen.getByLabelText("開始日期"), { target: { value: "2025-01-01" } });
    expect(screen.getByText("查詢範圍最多 366 天。")).toBeTruthy();
    expect(screen.queryByRole("link", { name: "匯出 CSV" })).toBeNull();
    expect((screen.getByRole("button", { name: "套用" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("開始日期"), { target: { value: "2026-11-10" } });
    await user.click(screen.getByRole("button", { name: "套用" }));
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ from: "2026-11-10", to: "2026-11-23" }),
    );
  });

  it("明細依 cursor 載入下一頁", async () => {
    const api = costsApi();
    signIn();
    const user = userEvent.setup();
    renderRoute("/costs");
    await screen.findByText("影片 1");
    await user.click(screen.getByRole("button", { name: "載入更多" }));
    expect(await screen.findByText("影片 3")).toBeTruthy();
    const calls = api.calls("GET", "/api/costs/entries");
    expect(calls.at(-1)!.url.searchParams.get("cursor")).toBe("c2");
    expect(screen.queryByRole("button", { name: "載入更多" })).toBeNull();
  });

  it("覆寫已有的單價要先確認舊價 → 新價；移除覆寫也要確認", async () => {
    const api = costsApi({
      "PUT /api/prices": (req) => ({
        ...(req.body as object),
        source: "override",
        checked_at: "2026-11-23",
        stale: false,
      }),
      "DELETE /api/prices": () => ({ ok: true }),
    });
    signIn();
    const user = userEvent.setup();
    renderRoute("/costs");
    const prices = await screen.findByRole("table", { name: "計價表" });
    const row = within(prices).getByText("claude-test").closest("tr")!;
    await user.click(within(row).getByRole("button", { name: "覆寫單價" }));
    const dialog = await screen.findByRole("dialog", { name: "設定單價" });
    const price = within(dialog).getByLabelText("單價（美元）");
    await user.clear(price);
    await user.type(price, "2.5");
    await user.click(within(dialog).getByRole("button", { name: "儲存單價" }));
    expect(await within(dialog).findByText(/US\$3\.00 → US\$2\.50/)).toBeTruthy();
    expect(api.calls("PUT", "/api/prices")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "確認覆寫" }));
    await waitFor(() => expect(api.calls("PUT", "/api/prices")).toHaveLength(1));
    expect(JSON.parse(String(api.calls("PUT", "/api/prices")[0]!.init.body))).toEqual({
      provider: "anthropic",
      model: "claude-test",
      unit_type: "input_mtok",
      usd_per_unit: 2.5,
    });

    const overrideRow = within(prices).getByText("gpt-test").closest("tr")!;
    await user.click(within(overrideRow).getByRole("button", { name: "移除覆寫" }));
    const confirm = await screen.findByRole("dialog", { name: "移除單價覆寫？" });
    await user.click(within(confirm).getByRole("button", { name: "移除覆寫" }));
    await waitFor(() => expect(api.calls("DELETE", "/api/prices")).toHaveLength(1));
    expect(JSON.parse(String(api.calls("DELETE", "/api/prices")[0]!.init.body))).toEqual({
      provider: "openai",
      model: "gpt-test",
      unit_type: "output_mtok",
    });
  });
});
