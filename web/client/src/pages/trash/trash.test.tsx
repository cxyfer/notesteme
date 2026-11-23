import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes } from "../../test/fixtures";
import { mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { trashed } from "../../test/ui7Fixtures";
import { daysLeft } from "./TrashPage";

describe("垃圾桶", () => {
  it("列出刪除時間與幾天後永久刪除；還原不確認（axe 無違規）", async () => {
    const api = mockApi({
      ...defaultRoutes(),
      "GET /api/trash": () => ({ items: [trashed(1), trashed(2)], total: 2 }),
      "POST /api/trash/{id}/restore": () => ({}),
    });
    signIn();
    const { container } = renderRoute("/trash");
    expect(await screen.findByText("刪掉的影片 1")).toBeTruthy();
    expect(screen.getAllByText(/5 天後永久刪除/)).toHaveLength(2);
    await expectNoA11yViolations(container);
    const row = screen.getByText("刪掉的影片 2").closest("li")!;
    await userEvent.click(within(row).getByRole("button", { name: "還原" }));
    expect(await screen.findByText("已還原。")).toBeTruthy();
    expect(api.calls("POST", "/api/trash/item-2/restore")).toHaveLength(1);
  });

  it("清空垃圾桶要先確認", async () => {
    const api = mockApi({ ...defaultRoutes(), "DELETE /api/trash": () => ({}) });
    signIn();
    renderRoute("/trash");
    await userEvent.click(await screen.findByRole("button", { name: "清空垃圾桶" }));
    const dialog = await screen.findByRole("dialog", { name: "清空垃圾桶中的 1 部影音？" });
    expect(within(dialog).getByText(/已花費的金額仍會保留在成本紀錄中/)).toBeTruthy();
    expect(api.calls("DELETE", "/api/trash")).toHaveLength(0);
    await userEvent.click(within(dialog).getByRole("button", { name: "清空垃圾桶" }));
    await waitFor(() => expect(api.calls("DELETE", "/api/trash")).toHaveLength(1));
  });

  it("空的垃圾桶顯示說明；天數無條件進位", async () => {
    mockApi({ ...defaultRoutes(), "GET /api/trash": () => ({ items: [], total: 0 }) });
    signIn();
    renderRoute("/trash");
    expect(await screen.findByText(/垃圾桶是空的/)).toBeTruthy();
    expect(daysLeft("2026-11-02T00:00:00Z", Date.parse("2026-11-01T01:00:00Z"))).toBe(1);
    expect(daysLeft("2026-11-01T00:00:00Z", Date.parse("2026-11-02T00:00:00Z"))).toBe(0);
  });
});
