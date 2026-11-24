import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { invalidationTable } from "../../events/invalidation";
import { expectNoA11yViolations } from "../../test/axe";
import { detail, item, queueStatus, stage } from "../../test/fixtures";
import { apiError, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { filterTasks, validateQueueSearch } from "./filters";

type Q = ReturnType<typeof queueStatus>;

function queueApi(q: () => Q | Response, routes: Record<string, MockHandler> = {}) {
  return mockApi({
    "GET /api/queue": () => q(),
    "GET /api/items/{id}": (req) =>
      detail(item(Number(req.params.id!.split("-")[1]), { id: req.params.id }), [
        stage("metadata", "done"),
      ]),
    "GET /api/items": (req) => {
      const st = req.url.searchParams.getAll("status");
      return { items: [], total: st.includes("correct:failed") ? 2 : 0 };
    },
    ...routes,
  });
}

async function openQueue(path = "/queue") {
  signIn();
  const r = renderRoute(path);
  await screen.findByRole("heading", { level: 1, name: "佇列" });
  await screen.findByRole("heading", { level: 2, name: "下載" });
  return r;
}

describe("佇列：通道", () => {
  it("每個資源類別一條通道：使用中、上限、等待；上限可以 +／−", async () => {
    let q = queueStatus();
    const api = queueApi(() => q, {
      "PATCH /api/queue/limits": (req) => {
        const body = req.body as Record<string, number>;
        q = {
          ...q,
          lanes: q.lanes!.map((l) => (body[l.class] ? { ...l, limit: body[l.class]! } : l)),
        };
        return q;
      },
    });
    const { container } = await openQueue();
    const lanes = screen.getByRole("list", { name: "資源類別" });
    const download = within(lanes).getAllByRole("listitem")[0]!;
    expect(within(download).getByText("使用中 1，上限 2，等待 0")).toBeTruthy();
    expect(download.querySelectorAll(".lane__slot")).toHaveLength(2);
    expect(download.querySelectorAll(".lane__slot[data-used]")).toHaveLength(1);
    expect(within(lanes).getByText("閒置，上限 1")).toBeTruthy();
    // 上限 1 不能再減
    expect(screen.getByRole("button", { name: "轉錄的上限減 1" })).toHaveProperty("disabled", true);
    await userEvent.click(screen.getByRole("button", { name: "下載的上限加 1" }));
    await waitFor(() => expect(api.calls("PATCH", "/api/queue/limits")).toHaveLength(1));
    expect(JSON.parse(api.calls("PATCH", "/api/queue/limits")[0]!.init.body as string)).toEqual({
      download: 3,
    });
    expect(await within(download).findByText("使用中 1，上限 3，等待 0")).toBeTruthy();
    await expectNoA11yViolations(container);
  });

  it("暫停整個佇列後顯示橫幅與「繼續處理」；也可以只暫停一個類別", async () => {
    let q = queueStatus();
    const api = queueApi(() => q, {
      "POST /api/queue/pause": (req) => {
        const cls = (req.body as { class?: string }).class;
        q = cls
          ? { ...q, lanes: q.lanes!.map((l) => (l.class === cls ? { ...l, paused: true } : l)) }
          : { ...q, paused: true };
        return q;
      },
      "POST /api/queue/resume": () => {
        q = { ...q, paused: false };
        return q;
      },
    });
    await openQueue();
    await userEvent.click(screen.getByRole("button", { name: "暫停LLM" }));
    expect(JSON.parse(api.calls("POST", "/api/queue/pause")[0]!.init.body as string)).toEqual({
      class: "llm",
    });
    expect(await screen.findByRole("button", { name: "繼續LLM" })).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "暫停佇列" }));
    expect(await screen.findByText("佇列已暫停，新任務不會開始。")).toBeTruthy();
    expect(JSON.parse(api.calls("POST", "/api/queue/pause")[1]!.init.body as string)).toEqual({});
    const resume = screen.getAllByRole("button", { name: "繼續處理" });
    await userEvent.click(resume[resume.length - 1]!);
    await waitFor(() => expect(screen.queryByText("佇列已暫停，新任務不會開始。")).toBeNull());
  });

  it("沒有權限時（403）用 toast 說明", async () => {
    queueApi(() => queueStatus(), {
      "POST /api/queue/pause": () => apiError(403, "forbidden", "需要 admin 權限。"),
    });
    await openQueue();
    await userEvent.click(screen.getByRole("button", { name: "暫停佇列" }));
    expect(await screen.findByText("需要 admin 權限。")).toBeTruthy();
  });

  it("佇列沒有啟用（501）時說明，不顯示控制項", async () => {
    signIn();
    queueApi(() => apiError(501, "not_implemented", "佇列尚未啟用。"));
    renderRoute("/queue");
    expect(await screen.findByText("這個功能還沒有做好。")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "暫停佇列" })).toBeNull();
    expect(screen.queryByRole("button", { name: "重試" })).toBeNull();
  });
});

describe("佇列：任務", () => {
  it("列出任務（標題來自詳情），點任務打開影音的紀錄分頁；說明 Agent 任務與單一任務動作的限制", async () => {
    queueApi(() => queueStatus());
    await openQueue();
    const list = await screen.findByRole("list", { name: "任務列表" });
    const rows = within(list).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    const link = await within(rows[0]!).findByRole("link", { name: "影片 1" });
    expect(link.getAttribute("href")).toBe("/items/item-1?tab=runs");
    expect(within(rows[0]!).getByText("處理中")).toBeTruthy();
    expect(within(rows[1]!).getByText("等待中")).toBeTruthy();
    expect(screen.getByText(/交給 Agent 的任務請到影音詳情/)).toBeTruthy();
    expect(screen.getByText(/「取消」只停止這個任務所屬的步驟/)).toBeTruthy();
  });

  it("「取消…」只取消這個任務所屬的步驟：確認後 POST /api/tasks/{id}/cancel", async () => {
    const api = queueApi(() => queueStatus(), {
      "POST /api/tasks/{id}/cancel": () => detail(item(2), []),
    });
    await openQueue();
    const list = await screen.findByRole("list", { name: "任務列表" });
    await userEvent.click(within(list).getAllByRole("button", { name: "取消…" })[1]!);
    const dialog = await screen.findByRole("dialog", { name: "取消這個步驟？" });
    expect(within(dialog).getByText(/「校正」會停止並標成「已略過」/)).toBeTruthy();
    expect(within(dialog).getByText(/其他步驟不動/)).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "取消這個步驟" }));
    await waitFor(() => expect(api.calls("POST", "/api/tasks/task-2/cancel")).toHaveLength(1));
    expect(api.calls("POST", "/api/items/item-2/cancel")).toHaveLength(0);
    expect(await screen.findByText("已取消這個步驟。")).toBeTruthy();
  });

  it("篩選存在網址；沒有符合的任務時可以清除", async () => {
    queueApi(() => queueStatus());
    const { router } = await openQueue("/queue?class=llm");
    const list = await screen.findByRole("list", { name: "任務列表" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(1);
    await userEvent.selectOptions(screen.getByLabelText("狀態"), "leased");
    expect(router.state.location.search).toEqual({ class: "llm", state: "leased" });
    expect(await screen.findByText("沒有符合條件的任務。")).toBeTruthy();
    await userEvent.click(screen.getAllByRole("button", { name: "清除篩選條件" })[0]!);
    await waitFor(() => expect(router.state.location.search).toEqual({}));
  });

  it("「停止處理」說明會停掉整部影音，確認後 POST /api/items/{id}/cancel", async () => {
    const api = queueApi(() => queueStatus(), {
      "POST /api/items/{id}/cancel": () => detail(item(1), []),
    });
    await openQueue();
    const list = await screen.findByRole("list", { name: "任務列表" });
    await userEvent.click(within(list).getAllByRole("button", { name: "停止處理…" })[0]!);
    const dialog = await screen.findByRole("dialog", { name: "停止處理這部影音？" });
    expect(within(dialog).getByText(/不是只停這一個任務/)).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "停止處理" }));
    await waitFor(() => expect(api.calls("POST", "/api/items/item-1/cancel")).toHaveLength(1));
  });

  it("需要你處理：依步驟計數，連到媒體庫的篩選", async () => {
    queueApi(() => queueStatus());
    await openQueue();
    const link = await screen.findByRole("link", { name: "校正：2 部" });
    expect(decodeURIComponent(link.getAttribute("href")!)).toContain("correct:failed");
  });

  it("SSE 讓佇列重新讀取；列表原地更新，不重新掛載", async () => {
    let q = queueStatus();
    queueApi(() => q);
    const { queryClient } = await openQueue();
    const list = await screen.findByRole("list", { name: "任務列表" });
    q = {
      ...q,
      tasks: q.tasks!.map((x) => (x.id === "task-2" ? { ...x, state: "leased" as const } : x)),
    };
    await act(async () => {
      for (const k of invalidationTable["stage.status"]({
        type: "stage.status",
        item: "yt:x",
        item_id: "item-2",
        stage: "correct",
        status: "running",
      }))
        await queryClient.invalidateQueries({ queryKey: k });
    });
    await waitFor(() => expect(within(list).getAllByText("處理中")).toHaveLength(2));
    expect(screen.getByRole("list", { name: "任務列表" })).toBe(list);
  });

  it("佇列是空的時邀請新增", async () => {
    queueApi(() => queueStatus({ tasks: [] }));
    await openQueue();
    expect(await screen.findByText("佇列是空的。新增影音，或等訂閱的頻道有新內容。")).toBeTruthy();
  });
});

describe("佇列：失敗與停住", () => {
  const failed = (id: string, n: number, over: Record<string, unknown> = {}) => ({
    id,
    item_id: `item-${n}`,
    item: `yt:vid0000000${n}`,
    stage: "correct",
    resource_class: "llm",
    status: "failed" as const,
    message: "重試多次仍然失敗。",
    attempts: 3,
    finished_at: "2026-11-24T01:00:00Z",
    ...over,
  });

  it("列出失敗、停住與已取消的步驟；重試送 POST /api/tasks/{id}/retry，成功後重新讀取佇列", async () => {
    let q = queueStatus({
      failed: [
        failed("t-a", 1),
        failed("t-b", 2, { status: "blocked", block_reason: "budget_blocked", message: "" }),
        failed("t-c", 3, { status: "skipped", block_reason: "cancelled", message: "已取消。" }),
      ],
    });
    const api = queueApi(() => q, {
      "POST /api/tasks/{id}/retry": (req) => {
        q = { ...q, failed: q.failed!.filter((f) => f.id !== req.params.id) };
        return detail(item(1), []);
      },
    });
    const { container } = await openQueue();
    const list = await screen.findByRole("list", { name: "失敗與停住的步驟" });
    expect(screen.getByRole("heading", { level: 2, name: "失敗與停住（3）" })).toBeTruthy();
    const rows = within(list).getAllByRole("listitem");
    expect(within(rows[0]!).getByText("失敗")).toBeTruthy();
    expect(within(rows[0]!).getByText("重試多次仍然失敗。")).toBeTruthy();
    expect(within(rows[1]!).getByText("超出預算")).toBeTruthy();
    expect(within(rows[2]!).getByText("已取消")).toBeTruthy();
    await userEvent.click(await within(rows[0]!).findByRole("button", { name: "重試 影片 1" }));
    await waitFor(() => expect(api.calls("POST", "/api/tasks/t-a/retry")).toHaveLength(1));
    expect(await screen.findByText("已重新排入佇列。")).toBeTruthy();
    await waitFor(() =>
      expect(screen.getByRole("heading", { level: 2, name: "失敗與停住（2）" })).toBeTruthy(),
    );
    await expectNoA11yViolations(container);
  });

  it("全部重試依序送出；有失敗時說明原因，其他照樣重試", async () => {
    const q = queueStatus({ failed: [failed("t-a", 1), failed("t-b", 2), failed("t-c", 3)] });
    const api = queueApi(() => q, {
      "POST /api/tasks/{id}/retry": (req) =>
        req.params.id === "t-b"
          ? apiError(409, "task_not_retryable", "這個步驟已經在處理中。")
          : detail(item(1), []),
    });
    await openQueue();
    await userEvent.click(await screen.findByRole("button", { name: "全部重試" }));
    await waitFor(() => expect(api.calls("POST", "/api/tasks/t-c/retry")).toHaveLength(1));
    expect(api.calls("POST", "/api/tasks/t-a/retry")).toHaveLength(1);
    expect(api.calls("POST", "/api/tasks/t-b/retry")).toHaveLength(1);
    expect(await screen.findByText(/已重新排入 2 個步驟。 這個步驟現在不能重試/)).toBeTruthy();
  });

  it("沒有失敗的步驟時說明，沒有「全部重試」", async () => {
    queueApi(() => queueStatus());
    await openQueue();
    expect(await screen.findByText("沒有失敗、停住或已取消的步驟。")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "全部重試" })).toBeNull();
  });
});

describe("佇列：篩選模型", () => {
  it("不合法的值丟掉；執行中的排前面", () => {
    expect(validateQueueSearch({ class: "llm", state: "x" })).toEqual({ class: "llm" });
    const tasks = queueStatus().tasks!;
    expect(filterTasks([...tasks].reverse(), {}).map((x) => x.id)).toEqual(["task-1", "task-2"]);
    expect(filterTasks(tasks, { state: "pending" }).map((x) => x.id)).toEqual(["task-2"]);
  });
});
