import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { queryKeys } from "../../api/queryKeys";
import { invalidationTable } from "../../events/invalidation";
import { dispatchServerEvent, dispatchStreamReconnect } from "../../events/useEventStream";
import { expectNoA11yViolations } from "../../test/axe";
import { detail, item, stage } from "../../test/fixtures";
import { apiError, mockApi, type MockRequest } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";

type Item = ReturnType<typeof item>;

/** 列表與詳情的替身：items 依 cursor 分頁（每頁 pageSize 筆） */
function libraryApi(
  all: Item[],
  opts: {
    pageSize?: number;
    stages?: (id: string) => ReturnType<typeof stage>[];
    list?: (req: MockRequest) => unknown;
  } = {},
) {
  const size = opts.pageSize ?? 100;
  return mockApi({
    "GET /api/items": (req) => {
      const custom = opts.list?.(req);
      if (custom !== undefined) return custom;
      const start = Number(req.url.searchParams.get("cursor") ?? 0);
      const page = all.slice(start, start + size);
      const next = start + size < all.length ? String(start + size) : undefined;
      return { items: page, total: all.length, ...(next ? { next_cursor: next } : {}) };
    },
    "GET /api/items/{id}": (req) => {
      const it = all.find((x) => x.id === req.params.id) ?? item(0, { id: req.params.id });
      return detail(it, opts.stages?.(it.id) ?? [stage("metadata", "done")]);
    },
    "POST /api/items/{id}/rerun": () => ({}),
    "GET /api/profiles": () => ({ profiles: [] }),
  });
}

const items = (n: number) => Array.from({ length: n }, (_, i) => item(i + 1));

async function openLibrary(path = "/library") {
  signIn();
  const r = renderRoute(path);
  await screen.findByRole("heading", { level: 1, name: "媒體庫" });
  return r;
}

describe("媒體庫：列表", () => {
  it("顯示總數與每一列；列連到影音詳情，各步驟狀態來自詳情", async () => {
    libraryApi(items(3), {
      stages: () => [stage("metadata", "done"), stage("transcript", "running")],
    });
    await openLibrary();
    expect(await screen.findByText("共 3 部")).toBeTruthy();
    const list = screen.getByRole("list", { name: "影音列表" });
    const rows = within(list).getAllByRole("listitem");
    expect(rows).toHaveLength(3);
    expect(rows[0]!.getAttribute("aria-setsize")).toBe("3");
    const link = within(rows[0]!).getByRole("link", { name: "影片 1" });
    expect(link.getAttribute("href")).toBe("/items/item-1");
    expect(await within(rows[0]!).findByText("字幕：處理中")).toBeTruthy();
    expect(
      within(rows[0]!).getByRole("img", { name: "字幕：處理中，校正：等待中，翻譯：等待中" }),
    ).toBeTruthy();
    // YouTube 縮圖：由影片 ID 推得，不送 Referer
    const img = rows[0]!.querySelector("img")!;
    expect(img.getAttribute("src")).toBe("https://i.ytimg.com/vi/vid00000001/mqdefault.jpg");
    expect(img.getAttribute("referrerpolicy")).toBe("no-referrer");
  });

  it("沒有影音時邀請新增；按鈕打開新增抽屜", async () => {
    libraryApi([]);
    await openLibrary();
    expect(
      await screen.findByText(/還沒有任何影音。貼上 YouTube、Bilibili 或 Podcast 網址/),
    ).toBeTruthy();
    // 導覽也有「新增」：按空狀態裡的那一個
    const empty = document.querySelector<HTMLElement>(".empty")!;
    await userEvent.click(within(empty).getByRole("button", { name: "新增" }));
    expect(await screen.findByRole("dialog", { name: "新增" })).toBeTruthy();
  });

  it("篩選結果為空時顯示「清除篩選條件」，按下後清除網址的條件（LIB @manual）", async () => {
    const api = libraryApi([]);
    const { router } = await openLibrary("/library?channel=UCnobody");
    expect(await screen.findByText("沒有符合條件的影音。")).toBeTruthy();
    expect(api.calls("GET", "/api/items")[0]!.url.searchParams.get("channel")).toBe("UCnobody");
    await userEvent.click(screen.getByRole("button", { name: "清除篩選條件" }));
    await waitFor(() => expect(router.state.location.search).toEqual({}));
  });

  it("捲到底時用 cursor 載入下一頁", async () => {
    const api = libraryApi(items(8), { pageSize: 5 });
    await openLibrary();
    await waitFor(() => expect(api.calls("GET", "/api/items")).toHaveLength(2));
    expect(api.calls("GET", "/api/items")[1]!.url.searchParams.get("cursor")).toBe("5");
    expect(await screen.findByRole("link", { name: "影片 8" })).toBeTruthy();
    expect(screen.getByText("已載入 8／共 8 部")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "載入更多" })).toBeNull();
  });

  it("游標失效（422）時從第一頁重新讀取", async () => {
    let expired = true;
    const api = libraryApi(items(8), {
      pageSize: 5,
      list: (req) => {
        if (req.url.searchParams.get("cursor") && expired) {
          expired = false;
          return apiError(422, "invalid_request", "游標不正確或已失效");
        }
        return undefined;
      },
    });
    await openLibrary();
    await waitFor(() => {
      const firsts = api
        .calls("GET", "/api/items")
        .filter((c) => !c.url.searchParams.has("cursor"));
      expect(firsts.length).toBeGreaterThanOrEqual(2);
    });
    expect(await screen.findByRole("link", { name: "影片 8" })).toBeTruthy();
  });

  it("虛擬化：10,000 列只渲染可視範圍加 overscan，也只讀那些列的詳情（NFR-PERF-10）", async () => {
    const api = libraryApi(items(10_000), { pageSize: 10_000 });
    await openLibrary();
    const list = await screen.findByRole("list", { name: "影音列表" });
    const rows = within(list).getAllByRole("listitem");
    // 可視 800px ÷ 估計 88px ≈ 10 列，加上 overscan 8
    expect(rows.length).toBeGreaterThan(5);
    expect(rows.length).toBeLessThanOrEqual(30);
    expect(rows[0]!.getAttribute("aria-setsize")).toBe("10000");
    await waitFor(() => expect(api.calls("GET", "/api/items/{id}").length).toBe(rows.length));
    expect(list.querySelectorAll("a").length).toBeLessThanOrEqual(30);
  });
});

describe("媒體庫：篩選 ↔ 網址", () => {
  it("網址的條件送到 API；不合法的值丟掉", async () => {
    const api = libraryApi([]);
    const { router } = await openLibrary(
      "/library?platform=youtube&error_class=bogus&nope=1&dur_min=5&published_from=2026-11-01",
    );
    await screen.findByText("沒有符合條件的影音。");
    const q = api.calls("GET", "/api/items")[0]!.url.searchParams;
    expect(q.get("platform")).toBe("youtube");
    expect(q.get("error_class")).toBeNull();
    expect(q.get("nope")).toBeNull();
    expect(q.get("duration_min")).toBe("300");
    expect(q.get("published_from")).toBe(new Date(2026, 10, 1).toISOString());
    // 已套用的條件可以個別移除
    expect(screen.getByText("平台是 YouTube")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "移除條件：平台是 YouTube" }));
    await waitFor(() => expect(router.state.location.search.platform).toBeUndefined());
  });

  it("選擇平台寫進網址並重新查詢；重新整理（重新渲染同一個網址）後條件保留（LIB-R2）", async () => {
    const api = libraryApi([]);
    const { router, unmount } = await openLibrary();
    await userEvent.selectOptions(screen.getByLabelText("平台"), "youtube");
    await waitFor(() => expect(router.state.location.search).toEqual({ platform: "youtube" }));
    expect(router.state.location.href).toContain("platform=youtube");
    await waitFor(() =>
      expect(api.calls("GET", "/api/items").at(-1)!.url.searchParams.get("platform")).toBe(
        "youtube",
      ),
    );
    const href = router.state.location.href;
    unmount();
    const again = await openLibrary(href);
    expect((screen.getByLabelText("平台") as HTMLSelectElement).value).toBe("youtube");
    expect(again.router.state.location.search).toEqual({ platform: "youtube" });
  });

  it("步驟狀態：同一步驟多選、不同步驟一起送出（重複的 status 參數）", async () => {
    const api = libraryApi([]);
    const { router } = await openLibrary();
    await userEvent.click(screen.getByRole("button", { name: "更多篩選" }));
    const metadata = screen.getByRole("group", { name: "取得資訊" });
    await userEvent.click(within(metadata).getByLabelText("完成"));
    const normalize = screen.getByRole("group", { name: "正規化" });
    await userEvent.click(within(normalize).getByLabelText("完成"));
    await userEvent.click(within(normalize).getByLabelText("失敗"));
    await waitFor(() =>
      expect(router.state.location.search.status).toEqual([
        "metadata:done",
        "normalize:done",
        "normalize:failed",
      ]),
    );
    await waitFor(() =>
      expect(api.calls("GET", "/api/items").at(-1)!.url.searchParams.getAll("status")).toEqual([
        "metadata:done",
        "normalize:done",
        "normalize:failed",
      ]),
    );
    expect(screen.getByRole("button", { name: "更多篩選（3）" })).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "清除全部" }));
    await waitFor(() => expect(router.state.location.search).toEqual({}));
  });

  it("標題搜尋延遲送出、不新增瀏覽紀錄；「/」聚焦搜尋框、Esc 清除", async () => {
    const api = libraryApi([]);
    const { router } = await openLibrary();
    const box = screen.getByLabelText("搜尋標題");
    act(() => (document.activeElement as HTMLElement | null)?.blur());
    fireEvent.keyDown(document.body, { key: "/" });
    expect(document.activeElement).toBe(box);
    await userEvent.type(box, "假装");
    await waitFor(() => expect(router.state.location.search).toEqual({ q: "假装" }));
    await waitFor(() =>
      expect(api.calls("GET", "/api/items").at(-1)!.url.searchParams.get("q")).toBe("假装"),
    );
    // 打字中的每個字不會各自送出
    expect(api.calls("GET", "/api/items").filter((c) => c.url.searchParams.has("q"))).toHaveLength(
      1,
    );
    await userEvent.keyboard("{Escape}");
    expect((box as HTMLInputElement).value).toBe("");
    await waitFor(() => expect(router.state.location.search).toEqual({}));
  });

  it("點列上的頻道只看這個頻道", async () => {
    libraryApi(items(1));
    const { router } = await openLibrary();
    await userEvent.click(await screen.findByRole("button", { name: "只看頻道 UCchan" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ channel: "UCchan" }));
  });
});

describe("媒體庫：分組（LIB-R4）", () => {
  it("預設依頻道分組：送出 group_by=channel，每組第一列上方顯示頻道名稱", async () => {
    const api = libraryApi([
      item(1, { channel_id: "UCa", channel_name: "頻道甲" }),
      item(2, { channel_id: "UCa", channel_name: "頻道甲" }),
      item(3, { channel_id: "UCb" }),
      item(4, { channel_id: undefined }),
    ]);
    await openLibrary();
    expect(await screen.findByRole("link", { name: "影片 4" })).toBeTruthy();
    expect(api.calls("GET", "/api/items")[0]!.url.searchParams.get("group_by")).toBe("channel");
    const heads = screen
      .getAllByRole("heading", { level: 2 })
      .filter((h) => h.classList.contains("library__group"))
      .map((h) => h.textContent);
    // 同一頻道只顯示一次；沒有名稱時用頻道 ID；沒有頻道另成一組
    expect(heads).toEqual(["頻道甲", "UCb", "沒有頻道"]);
    expect((screen.getByRole("combobox", { name: "分組" }) as HTMLSelectElement).value).toBe(
      "channel",
    );
  });

  it("切換成依狀態：寫進網址、重新查詢，組名來自 API 的 group", async () => {
    const api = libraryApi([item(1, { group: "failed" }), item(2, { group: "idle" })]);
    const { router } = await openLibrary();
    await screen.findByRole("link", { name: "影片 2" });
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "分組" }), "status");
    await waitFor(() => expect(router.state.location.search).toEqual({ group: "status" }));
    await waitFor(() =>
      expect(api.calls("GET", "/api/items").at(-1)!.url.searchParams.get("group_by")).toBe(
        "status",
      ),
    );
    expect(await screen.findByRole("heading", { level: 2, name: "有步驟失敗" })).toBeTruthy();
    expect(screen.getByRole("heading", { level: 2, name: "其他" })).toBeTruthy();
  });

  it("不分組：不送 group_by、沒有組名；切回預設時從網址移除、其他條件保留；不合法的 group 丟掉", async () => {
    const api = libraryApi(items(2));
    const { router } = await openLibrary("/library?group=none&platform=youtube");
    await screen.findByRole("link", { name: "影片 2" });
    expect(api.calls("GET", "/api/items")[0]!.url.searchParams.has("group_by")).toBe(false);
    expect(document.querySelector(".library__group")).toBeNull();
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "分組" }), "channel");
    await waitFor(() => expect(router.state.location.search).toEqual({ platform: "youtube" }));

    await act(() => router.navigate({ to: "/library", search: { group: "bogus" } as never }));
    await waitFor(() =>
      expect(api.calls("GET", "/api/items").at(-1)!.url.searchParams.get("group_by")).toBe(
        "channel",
      ),
    );
  });
});

describe("媒體庫：即時更新不打亂列表（LIB-R9）", () => {
  it("item.created 只累計「有 N 個新項目」，點擊後才重新載入", async () => {
    const api = libraryApi(items(2));
    await openLibrary();
    await screen.findByRole("link", { name: "影片 1" });
    const before = api.calls("GET", "/api/items").length;
    act(() => {
      for (let i = 0; i < 3; i++)
        dispatchServerEvent({ type: "item.created", item: `yt:n${i}`, item_id: `n${i}` });
    });
    const notice = await screen.findByRole("button", { name: "有 3 個新項目" });
    expect(api.calls("GET", "/api/items")).toHaveLength(before);
    await userEvent.click(notice);
    await waitFor(() => expect(api.calls("GET", "/api/items")).toHaveLength(before + 1));
    expect(screen.queryByRole("button", { name: /個新項目/ })).toBeNull();
  });

  it("stage.status 只讓那一列的詳情重新讀取，列表不重新查詢", async () => {
    let status = "running";
    const api = libraryApi(items(2), {
      stages: (id) => [stage("transcript", id === "item-1" ? status : "done")],
    });
    const { queryClient } = await openLibrary();
    expect(await screen.findByText("字幕：處理中")).toBeTruthy();
    const lists = api.calls("GET", "/api/items").length;
    status = "failed";
    const ev = { type: "stage.status", item: "yt:x", item_id: "item-1" } as const;
    await act(async () => {
      for (const k of invalidationTable["stage.status"](ev))
        await queryClient.invalidateQueries({ queryKey: k });
    });
    expect(await screen.findByText("字幕：失敗")).toBeTruthy();
    expect(api.calls("GET", "/api/items")).toHaveLength(lists);
    expect(queryClient.getQueryState(queryKeys.items.detail("item-2"))?.dataUpdateCount).toBe(1);
  });

  it("重新連上後提示列表可能不是最新的", async () => {
    libraryApi(items(1));
    await openLibrary();
    await screen.findByRole("link", { name: "影片 1" });
    act(() => dispatchStreamReconnect());
    expect(await screen.findByRole("button", { name: /列表可能不是最新的/ })).toBeTruthy();
  });

  it("失敗只宣告一次（頁面層級的 aria-live）", async () => {
    libraryApi(items(1));
    await openLibrary();
    act(() =>
      dispatchServerEvent({
        type: "stage.status",
        item: "yt:x",
        item_id: "item-1",
        stage: "correct",
        status: "failed",
        title: "影片 1",
      }),
    );
    expect(await screen.findByText("「影片 1」校正失敗")).toBeTruthy();
  });
});

describe("媒體庫：需要處理的列與鍵盤", () => {
  it("失敗的步驟顯示錯誤類別、訊息與「從該步驟重新處理」", async () => {
    const api = libraryApi(items(1), {
      stages: () => [
        stage("transcript", "done"),
        stage("correct", "failed", { error_class: "permanent", message: "第 7 段驗證失敗 3 次" }),
      ],
    });
    await openLibrary();
    const row = within(await screen.findByRole("list", { name: "影音列表" })).getAllByRole(
      "listitem",
    )[0]!;
    expect(await within(row).findByText("校正：失敗")).toBeTruthy();
    expect(within(row).getByText("校正：無法完成。第 7 段驗證失敗 3 次")).toBeTruthy();
    // 列裡的錯誤說明不是 role=alert（捲動時重新掛載不能一直宣告）
    expect(within(row).queryByRole("alert")).toBeNull();
    await userEvent.click(within(row).getByRole("button", { name: "從校正重新處理" }));
    await waitFor(() => expect(api.calls("POST", "/api/items/{id}/rerun")).toHaveLength(1));
    const call = api.calls("POST", "/api/items/{id}/rerun")[0]!;
    expect(call.url.pathname).toBe("/api/items/item-1/rerun");
    expect(JSON.parse(String(call.init.body))).toEqual({ from: "correct" });
    expect(await screen.findByText("已排入重新處理，從校正開始")).toBeTruthy();
  });

  it("需要你處理（blocked）依原因顯示，並提供前往設定", async () => {
    libraryApi(items(1), {
      stages: () => [
        stage("transcript", "blocked", {
          block_reason: "needs_action",
          error_class: "needs_action",
        }),
      ],
    });
    const { router } = await openLibrary();
    const row = within(await screen.findByRole("list", { name: "影音列表" })).getAllByRole(
      "listitem",
    )[0]!;
    expect(await within(row).findByText("字幕：需要你處理")).toBeTruthy();
    await userEvent.click(within(row).getByRole("button", { name: "開啟帳號與 Cookie 設定" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/accounts"));
  });

  it("↑／↓／Home／End 在列之間移動焦點（roving tabindex），Enter 打開", async () => {
    libraryApi(items(5));
    const { router } = await openLibrary();
    const first = await screen.findByRole("link", { name: "影片 1" });
    const second = screen.getByRole("link", { name: "影片 2" });
    expect(first.tabIndex).toBe(0);
    expect(second.tabIndex).toBe(-1);
    first.focus();
    await userEvent.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(second);
    await userEvent.keyboard("{End}");
    await waitFor(() =>
      expect(document.activeElement).toBe(screen.getByRole("link", { name: "影片 5" })),
    );
    await userEvent.keyboard("{Home}");
    expect(document.activeElement).toBe(first);
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(router.state.location.pathname).toBe("/items/item-1"));
  });

  it("有資料、有篩選、有錯誤的列時沒有 axe 問題", async () => {
    libraryApi(items(3), {
      stages: (id) =>
        id === "item-2"
          ? [stage("correct", "failed", { error_class: "retryable", message: "逾時" })]
          : [stage("transcript", "done")],
    });
    await openLibrary("/library?platform=youtube");
    await screen.findByText("校正：失敗");
    await userEvent.click(screen.getByRole("button", { name: "更多篩選" }));
    await expectNoA11yViolations(document.body);
  });
});
