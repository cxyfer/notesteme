// 儲存檢視（LIB-R26～R28）：存成檢視、名稱重複的錯誤、套用會整組取代網址參數、刪除先確認。
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { item } from "../../test/fixtures";
import { apiError, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";

const VIEWS = {
  views: [
    {
      id: "v1",
      name: "等你決定",
      query: { blocked: "awaiting_user", group: "status" },
      created_at: "2026-11-25T00:00:00Z",
    },
    {
      id: "v2",
      name: "YouTube",
      query: { platform: "youtube", status: ["metadata:done"] },
      created_at: "2026-11-25T00:01:00Z",
    },
  ],
};

function viewsApi(routes: Record<string, MockHandler> = {}) {
  return mockApi({
    "GET /api/items": () => ({ items: [item(1)], total: 1 }),
    "GET /api/items/{id}": () => ({}),
    "GET /api/profiles": () => ({ profiles: [] }),
    "GET /api/views": () => VIEWS,
    ...routes,
  });
}

async function openLibrary(path: string) {
  signIn();
  const r = renderRoute(path);
  await screen.findByRole("heading", { level: 1, name: "媒體庫" });
  return r;
}

describe("媒體庫：儲存檢視", () => {
  it("有篩選時可以存成檢視：送出篩選與分組，不送顯示方式", async () => {
    let body: unknown;
    let reads = 0;
    viewsApi({
      "GET /api/views": () => {
        reads++;
        return { views: [] };
      },
      "POST /api/views": (req) => {
        body = req.body;
        return { id: "v9", name: "B", query: {}, created_at: "2026-11-25T00:00:00Z" };
      },
    });
    await openLibrary("/library?platform=bilibili&dur_max=30&group=published&view=grid");
    await userEvent.click(await screen.findByRole("button", { name: "存成檢視" }));
    const dialog = await screen.findByRole("dialog", { name: "存成檢視" });
    await expectNoA11yViolations(document.body);
    await userEvent.type(within(dialog).getByLabelText("名稱"), "  B站短片 ");
    await userEvent.click(within(dialog).getByRole("button", { name: "儲存" }));
    await waitFor(() =>
      expect(body).toEqual({
        name: "B站短片",
        query: { platform: "bilibili", dur_max: 30, group: "published" },
      }),
    );
    expect(await screen.findByText("已儲存檢視「B站短片」")).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(reads).toBeGreaterThanOrEqual(2);
  });

  it("沒有篩選時不顯示「存成檢視」；沒有已存的檢視時不顯示清單", async () => {
    viewsApi({ "GET /api/views": () => ({ views: [] }) });
    await openLibrary("/library");
    expect(await screen.findByText("共 1 部")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "存成檢視" })).toBeNull();
    expect(screen.queryByRole("heading", { name: "已存的檢視" })).toBeNull();
  });

  it("名稱重複：錯誤顯示在對話框裡，對話框不關", async () => {
    viewsApi({
      "POST /api/views": () => apiError(409, "view_exists", "已經有名稱是「YouTube」的檢視。"),
    });
    await openLibrary("/library?platform=youtube");
    await userEvent.click(await screen.findByRole("button", { name: "存成檢視" }));
    const dialog = await screen.findByRole("dialog", { name: "存成檢視" });
    await userEvent.type(within(dialog).getByLabelText("名稱"), "YouTube");
    await userEvent.click(within(dialog).getByRole("button", { name: "儲存" }));
    expect(await within(dialog).findByText("已經有同名的檢視。換一個名稱。")).toBeTruthy();
  });

  it("點已存的檢視：網址參數整組換成檢視的內容，保留顯示方式，標出套用中的檢視", async () => {
    viewsApi();
    const { router } = await openLibrary("/library?q=hello&read_state=unread&view=grid");
    const list = await screen.findByRole("region", { name: "已存的檢視" });
    await userEvent.click(within(list).getByRole("button", { name: "等你決定" }));
    await waitFor(() =>
      expect(router.state.location.search).toEqual({
        blocked: "awaiting_user",
        group: "status",
        view: "grid",
      }),
    );
    expect(
      within(list).getByRole("button", { name: "等你決定" }).getAttribute("aria-pressed"),
    ).toBe("true");
    expect(within(list).getByRole("button", { name: "YouTube" }).getAttribute("aria-pressed")).toBe(
      "false",
    );
    await expectNoA11yViolations(document.body);
  });

  it("刪除檢視要先確認", async () => {
    const deleted: string[] = [];
    let views = VIEWS.views;
    viewsApi({
      "GET /api/views": () => ({ views }),
      "DELETE /api/views/{id}": (req) => {
        deleted.push(req.params.id!);
        views = views.filter((v) => v.id !== req.params.id);
        return new Response(null, { status: 204 });
      },
    });
    await openLibrary("/library");
    const list = await screen.findByRole("region", { name: "已存的檢視" });
    await userEvent.click(within(list).getByRole("button", { name: "刪除檢視「YouTube」" }));
    const dialog = await screen.findByRole("dialog", { name: "刪除檢視「YouTube」？" });
    expect(deleted).toEqual([]);
    await userEvent.click(within(dialog).getByRole("button", { name: "刪除" }));
    await waitFor(() => expect(deleted).toEqual(["v2"]));
    expect(await screen.findByText("已刪除檢視「YouTube」")).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("button", { name: "YouTube" })).toBeNull());
  });
});
