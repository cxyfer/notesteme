import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { detail, item, stage } from "../../test/fixtures";
import { mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { runChunks } from "./bulk";
import { toApiQuery, validateLibrarySearch } from "./filters";

type Body = {
  action: string;
  ids: string[];
  dry_run?: boolean;
  from?: string;
  profile?: string;
  delete_outputs?: boolean;
};

const all = [item(1), item(2), item(3)];

function bulkApi(batch: (b: Body) => unknown, extra: Record<string, MockHandler> = {}) {
  const bodies: Body[] = [];
  const api = mockApi({
    "GET /api/items": () => ({ items: all, total: all.length }),
    "GET /api/items/{id}": (req) =>
      detail(all.find((x) => x.id === req.params.id) ?? item(9), [stage("metadata", "done")]),
    "GET /api/profiles": () => ({
      profiles: [{ name: "default", builtin: true, default: true, config: {}, created_at: "" }],
    }),
    "POST /api/items/batch": (req) => {
      const b = req.body as Body;
      bodies.push(b);
      return batch(b);
    },
    ...extra,
  });
  return { api, bodies };
}

async function openAndPick(n: number, path = "/library") {
  signIn();
  const r = renderRoute(path);
  const user = userEvent.setup();
  for (let i = 1; i <= n; i++)
    await user.click(await screen.findByRole("checkbox", { name: `選取「影片 ${i}」` }));
  return { ...r, user };
}

describe("媒體庫：批次操作（LIB-08）", () => {
  it("刪除：確認框顯示合計與輸出檔選項；部分失敗時列出失敗的影音與原因（axe 無違規）", async () => {
    const { bodies } = bulkApi((b) =>
      b.dry_run
        ? {
            action: "delete",
            dry_run: true,
            count: 3,
            items: b.ids.map((id, i) => ({
              id,
              ref: `yt:${id}`,
              ok: true,
              title: `影片 ${i + 1}`,
            })),
            estimated_usd: 0,
            deletion: {
              artifacts: 12,
              bytes: 3_000_000,
              spent_usd: 0.42,
              outputs: 4,
              modified_outputs: 1,
            },
          }
        : {
            action: "delete",
            dry_run: false,
            count: b.ids.length,
            succeeded: 2,
            failed: 1,
            items: b.ids.map((id, i) =>
              i === 1
                ? {
                    id,
                    ref: `yt:${id}`,
                    ok: false,
                    code: "in_trash",
                    message: "這部影音在垃圾桶中。",
                  }
                : { id, ref: `yt:${id}`, ok: true },
            ),
          },
    );
    const { user } = await openAndPick(3);
    const bar = screen.getByRole("region", { name: "批次操作" });
    expect(within(bar).getByText("已選取 3 部")).toBeTruthy();
    await user.click(await within(bar).findByRole("button", { name: "刪除" }));
    const dialog = await screen.findByRole("dialog", { name: "刪除：已選取 3 部" });
    expect(await within(dialog).findByText("3 部影音")).toBeTruthy();
    expect(within(dialog).getByText(/已花費 US\$0\.42/)).toBeTruthy();
    expect(within(dialog).getByText(/可釋放/)).toBeTruthy();
    await expectNoA11yViolations(document.body);
    // N = 輸出檔總數減去改過的
    await user.click(
      within(dialog).getByRole("checkbox", { name: /同時刪除未修改過的輸出檔（3 個）/ }),
    );
    await user.click(within(dialog).getByRole("button", { name: "刪除 3 部" }));
    expect(await within(dialog).findByText("成功 2 部，失敗 1 部")).toBeTruthy();
    expect(within(dialog).getByText("影片 2")).toBeTruthy();
    expect(within(dialog).getByText("這部影音在垃圾桶中。")).toBeTruthy();
    expect(bodies[0]).toMatchObject({ action: "delete", dry_run: true });
    expect(bodies[0]!.delete_outputs).toBeUndefined();
    expect(bodies[1]).toEqual({
      action: "delete",
      ids: ["item-1", "item-2", "item-3"],
      delete_outputs: true,
    });
  });

  it("重新處理：選步驟、預估費用、超過預算的警告；dry_run 不能處理的影音不送出", async () => {
    const { bodies } = bulkApi((b) =>
      b.dry_run
        ? {
            action: "rerun",
            dry_run: true,
            count: 1,
            items: [
              { id: "item-1", ref: "yt:1", ok: true, title: "影片 1" },
              { id: "item-2", ref: "yt:2", ok: false, title: "影片 2", message: "不能重跑。" },
            ],
            estimated_usd: 1.5,
            unknown_estimates: 2,
            budget: { remaining_usd: 1, exceeds: true },
          }
        : {
            action: "rerun",
            dry_run: false,
            count: 1,
            succeeded: 1,
            failed: 0,
            items: [{ id: "item-1", ref: "yt:1", ok: true }],
          },
    );
    const { user } = await openAndPick(2);
    await user.click(await screen.findByRole("button", { name: "重新處理" }));
    const dialog = await screen.findByRole("dialog", { name: "重新處理：已選取 2 部" });
    await user.selectOptions(within(dialog).getByLabelText("從哪個步驟開始"), "correct");
    await user.click(within(dialog).getByRole("button", { name: "查看影響範圍" }));
    expect(await within(dialog).findByText(/預估花費 US\$1\.50/)).toBeTruthy();
    expect(within(dialog).getByText("另有 2 個任務無法預估費用")).toBeTruthy();
    expect(within(dialog).getByText(/超過本月剩餘的預算/)).toBeTruthy();
    expect(within(dialog).getByText("不能重跑。")).toBeTruthy();
    // 超過預算只警告，不擋確認（LIB-R17）
    await user.click(within(dialog).getByRole("button", { name: "重新處理 1 部" }));
    expect(await within(dialog).findByText("成功 1 部，失敗 0 部")).toBeTruthy();
    expect(bodies[0]).toMatchObject({ action: "rerun", from: "correct", dry_run: true });
    expect(bodies[1]).toEqual({ action: "rerun", ids: ["item-1"], from: "correct" });
  });

  it("標為已讀直接執行，toast 的「復原」標回未讀", async () => {
    const { bodies } = bulkApi((b) => ({
      action: b.action,
      dry_run: false,
      count: b.ids.length,
      succeeded: b.ids.length,
      failed: 0,
      items: b.ids.map((id) => ({ id, ref: id, ok: true })),
    }));
    const { user } = await openAndPick(2);
    await user.click(await screen.findByRole("button", { name: "標為已讀" }));
    expect(await screen.findByText("已將 2 部標為已讀")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "復原" }));
    await waitFor(() => expect(bodies.map((b) => b.action)).toEqual(["mark_read", "mark_unread"]));
    expect(bodies[1]!.ids).toEqual(["item-1", "item-2"]);
  });

  it("換篩選時清除選取；「選取符合條件的全部」用同一組條件讀出所有 ID", async () => {
    const { api } = bulkApi(() => ({}), {
      "GET /api/items": (req) =>
        req.url.searchParams.get("limit") === "200"
          ? { items: [...all, item(4)], total: 4 }
          : { items: all, total: 4, next_cursor: "c" },
    });
    const { user, router } = await openAndPick(1, "/library?platform=youtube");
    await user.click(await screen.findByRole("button", { name: "選取符合條件的全部 4 部" }));
    expect(await screen.findByText("已選取 4 部")).toBeTruthy();
    const q = api
      .calls("GET", "/api/items")
      .find((c) => c.url.searchParams.get("limit") === "200")!;
    expect(q.url.searchParams.get("platform")).toBe("youtube");
    await router.navigate({ to: "/library", search: { platform: "bilibili" } });
    await waitFor(() => expect(screen.queryByRole("region", { name: "批次操作" })).toBeNull());
  });

  it("等你決定：勾選兩部後繼續處理，對話框顯示數量與預估費用，只送出勾選的（DIG-R7）", async () => {
    const continued: string[][] = [];
    const { bodies } = bulkApi(
      (b) => ({
        action: "rerun",
        dry_run: true,
        count: b.ids.length,
        items: b.ids.map((id) => ({ id, ref: id, ok: true })),
        estimated_usd: 0.3,
      }),
      {
        "POST /api/items/continue": (req) => {
          const ids = (req.body as { ids: string[] }).ids;
          continued.push(ids);
          return { continued: ids, skipped: [] };
        },
      },
    );
    const { user } = await openAndPick(2, "/library?blocked=awaiting_user");
    // 有選取時不顯示「繼續處理已載入的」
    expect(screen.queryByRole("button", { name: /繼續處理已載入的/ })).toBeNull();
    const bar = screen.getByRole("region", { name: "批次操作" });
    await user.click(await within(bar).findByRole("button", { name: "繼續處理" }));
    const dialog = await screen.findByRole("dialog", { name: "繼續處理：已選取 2 部" });
    expect(await within(dialog).findByText(/預估花費約 US\$0\.30/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "繼續處理 2 部" }));
    expect(await within(dialog).findByText("成功 2 部，失敗 0 部")).toBeTruthy();
    expect(continued).toEqual([["item-1", "item-2"]]);
    expect(bodies[0]).toMatchObject({ action: "rerun", from: "segment", dry_run: true });
  });

  it("已讀狀態的篩選在 URL 並送給 API（LIB-R19）", () => {
    expect(validateLibrarySearch({ read_state: "unread" }).read_state).toBe("unread");
    expect(validateLibrarySearch({ read_state: "x" }).read_state).toBeUndefined();
    expect(toApiQuery({ read_state: "read" }).read_state).toBe("read");
  });

  it("分批執行：停止後剩下的標成 cancelled", async () => {
    let stop = false;
    const out = await runChunks(
      ["a", "b", "c"],
      1,
      async (c) => {
        stop = true;
        return c.map((id) => ({ id, ref: id, ok: true }));
      },
      { shouldStop: () => stop, cancelledMessage: "沒有處理" },
    );
    expect(out.succeeded).toBe(1);
    expect(out.cancelled).toBe(2);
    expect(out.items.map((x) => x.code ?? "ok")).toEqual(["ok", "cancelled", "cancelled"]);
  });
});
