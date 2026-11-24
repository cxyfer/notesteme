import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import i18next from "i18next";
import { describe, expect, it, vi } from "vitest";

import { invalidationTable } from "../../events/invalidation";
import { dispatchServerEvent } from "../../events/useEventStream";
import { expectNoA11yViolations } from "../../test/axe";
import { cue, detail, item, run, stage } from "../../test/fixtures";
import { apiError, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";

type Cue = ReturnType<typeof cue>;
type Stage = ReturnType<typeof stage>;

const SOURCE: Cue[] = [
  cue(1, 0, 2, "比如说daily stand-up"),
  cue(2, 2, 4, "原本的目的是"),
  cue(3, 4, 6, "提升团队的信息同步"),
  cue(4, 65, 68, "第六十五秒的句子"),
  cue(5, 70, 72, "还没校正的句子", { timing: "silence" }),
];
const CORRECTED: Cue[] = [
  cue(1, 0, 2, "比如說 daily stand-up"),
  // 校正把第 2、3 句合併
  cue(2, 2, 6, "原本的目的是提升團隊的資訊同步"),
  cue(3, 65, 68, "第六十五秒的句子"),
];
const TRANSLATED: Cue[] = [
  cue(1, 0, 2, "For example, the daily stand-up"),
  cue(2, 65, 68, "A line at 65s"),
];

const DONE_ALL: Stage[] = [
  stage("metadata", "done"),
  stage("transcript", "done", { executor: "local" }),
  stage("normalize", "done", { executor: "opencc" }),
  stage("segment", "done"),
  stage("correct", "done", { executor: "api" }),
  stage("translate", "done", { executor: "api" }),
];

function itemApi(
  opts: {
    platform?: string;
    stages?: Stage[];
    variants?: Record<string, { lang: string; cues: Cue[] }>;
    routes?: Record<string, MockHandler>;
  } = {},
) {
  const it = item(1, {
    platform: opts.platform ?? "youtube",
    external_id: "abcdEFGhijk",
    original_lang: "zh-Hans",
    duration_s: 120,
    title: "我们都只是在假装着做Agile",
  });
  const variants = opts.variants ?? {
    source: { lang: "zh-Hans", cues: SOURCE },
    normalized: { lang: "zh-Hans", cues: SOURCE },
    segmented: { lang: "zh-Hans", cues: SOURCE },
    corrected: { lang: "zh-Hant", cues: CORRECTED },
    translated: { lang: "en", cues: TRANSLATED },
  };
  return mockApi({
    "GET /api/items/{id}": (req) =>
      req.params.id === "item-1" || req.params.id === "yt:abcdEFGhijk"
        ? detail(it, opts.stages ?? DONE_ALL)
        : apiError(404, "not_found"),
    "GET /api/items/{id}/cues": (req) => {
      const v = req.url.searchParams.get("variant") ?? "corrected";
      const found = variants[v];
      return found
        ? { variant: v, lang: found.lang, cues: found.cues }
        : apiError(404, "not_found");
    },
    "GET /api/items/{id}/runs": () => ({
      runs: [
        run("correct", {
          config: {
            language: { targets: ["en"] },
            stages: {
              correct: {
                executor: "api",
                provider: "anthropic",
                model: "fixture-model",
                prompt: "correct-default",
              },
            },
          },
        }),
        run("transcript"),
      ],
    }),
    "GET /api/items/{id}/outputs": () => ({ outputs: [] }),
    "GET /api/profiles": () => ({
      profiles: [
        {
          name: "default",
          version: 2,
          config: { stages: { correct: { provider: "anthropic" } } },
          builtin: true,
          default: true,
          created_at: "2026-11-01T00:00:00Z",
          updated_at: "2026-11-01T00:00:00Z",
        },
      ],
    }),
    "GET /api/prompts": () => ({
      prompts: [
        {
          id: "correct-default",
          kind: "correct",
          builtin: true,
          default_version: 1,
          latest_version: 2,
          created_at: "",
          updated_at: "",
        },
        {
          id: "translate-default",
          kind: "translate",
          builtin: true,
          default_version: 1,
          latest_version: 1,
          created_at: "",
          updated_at: "",
        },
      ],
    }),
    ...opts.routes,
  });
}

async function openItem(path = "/items/item-1") {
  signIn();
  const r = renderRoute(path);
  await screen.findByRole("heading", { level: 1, name: "我们都只是在假装着做Agile" });
  return r;
}

describe("影音詳情：標題與時間帶", () => {
  it("標題帶原文的 lang；時間帶是 slider，方向鍵移動播放頭並讓目前句高亮", async () => {
    itemApi();
    const { container } = await openItem();
    expect(screen.getByRole("heading", { level: 1 }).getAttribute("lang")).toBe("zh-Hans");
    expect(screen.getByRole("link", { name: "在 YouTube 開啟" })).toBeTruthy();
    const slider = screen.getByRole("slider", { name: "時間帶：播放位置" });
    expect(slider.getAttribute("aria-valuetext")).toBe("播放位置 00:00");
    // 文字版的步驟狀態（不只靠顏色）
    expect(container.querySelector(".sband__labels")?.textContent).toContain("校正：完成");
    await screen.findByText("比如說 daily stand-up");
    slider.focus();
    for (let i = 0; i < 13; i++) await userEvent.keyboard("{ArrowRight}");
    expect(slider.getAttribute("aria-valuetext")).toBe("播放位置 01:05");
    const current = await screen.findByRole("button", { name: "跳到 01:05（目前播放的句子）" });
    expect(current.getAttribute("aria-current")).toBe("true");
  });

  it("用影音代號進來也能顯示，之後的 API 一律用影音 ID", async () => {
    const api = itemApi();
    await openItem("/items/yt:abcdEFGhijk");
    await screen.findByText("比如說 daily stand-up");
    expect(api.calls("GET", "/api/items/item-1/cues").length).toBeGreaterThan(0);
    expect(api.calls("GET", "/api/items/yt:abcdEFGhijk/cues")).toHaveLength(0);
  });

  it("數字鍵 4 切到紀錄分頁（README §3.6）", async () => {
    itemApi();
    const { router } = await openItem();
    await userEvent.keyboard("4");
    await waitFor(() => expect(router.state.location.search).toMatchObject({ tab: "runs" }));
  });

  it("標為已讀用批次 API 送這一部（LIB-R19；打開詳情不會自動標為已讀）", async () => {
    const bodies: unknown[] = [];
    itemApi({
      routes: {
        "POST /api/items/batch": (req) => {
          bodies.push(req.body);
          return {
            action: "mark_read",
            dry_run: false,
            count: 1,
            items: [{ id: "item-1", ref: "x", ok: true }],
          };
        },
      },
    });
    await openItem();
    await userEvent.click(screen.getByRole("button", { name: "標為已讀" }));
    expect(await screen.findByText("已將 1 部標為已讀")).toBeTruthy();
    expect(bodies).toEqual([{ action: "mark_read", ids: ["item-1"] }]);
  });

  it("影音在垃圾桶中時提供還原", async () => {
    let trashed = true;
    const api = itemApi({
      routes: {
        "GET /api/items/{id}": () =>
          trashed
            ? apiError(404, "in_trash", "x", { item_id: "item-1" })
            : detail(item(1, { title: "我们都只是在假装着做Agile" }), DONE_ALL),
        "POST /api/trash/{id}/restore": () => {
          trashed = false;
          return {};
        },
      },
    });
    signIn();
    renderRoute("/items/item-1");
    await userEvent.click(await screen.findByRole("button", { name: "從垃圾桶還原" }));
    expect(api.calls("POST", "/api/trash/item-1/restore")).toHaveLength(1);
    expect(
      await screen.findByRole("heading", { level: 1, name: "我们都只是在假装着做Agile" }),
    ).toBeTruthy();
  });

  it("找不到影音時顯示說明與回到媒體庫", async () => {
    itemApi();
    signIn();
    renderRoute("/items/nope");
    expect(await screen.findByText("找不到這部影音。它可能已經被刪除。")).toBeTruthy();
    expect(screen.getByRole("button", { name: "回到媒體庫" })).toBeTruthy();
  });
});

describe("影音詳情：播放器", () => {
  it("YouTube 用 nocookie 嵌入（enablejsapi、referrerpolicy）；點字幕句用 postMessage 跳播", async () => {
    itemApi();
    const { container } = await openItem();
    const frame = container.querySelector("iframe")!;
    const src = new URL(frame.src);
    expect(src.origin).toBe("https://www.youtube-nocookie.com");
    expect(src.pathname).toBe("/embed/abcdEFGhijk");
    expect(src.searchParams.get("enablejsapi")).toBe("1");
    expect(frame.getAttribute("referrerpolicy")).toBe("strict-origin-when-cross-origin");
    const post = vi.spyOn(frame.contentWindow!, "postMessage");
    // 播放器回應過才用 postMessage（來源不對的訊息不算）
    act(() => {
      window.dispatchEvent(
        new MessageEvent("message", {
          origin: "https://evil.example",
          source: frame.contentWindow,
          data: JSON.stringify({ event: "infoDelivery", info: { currentTime: 999 } }),
        }),
      );
      window.dispatchEvent(
        new MessageEvent("message", {
          origin: "https://www.youtube-nocookie.com",
          source: frame.contentWindow,
          data: JSON.stringify({ event: "infoDelivery", info: { currentTime: 3 } }),
        }),
      );
    });
    expect(screen.getByRole("slider").getAttribute("aria-valuetext")).toBe("播放位置 00:03");
    await userEvent.click(await screen.findByRole("button", { name: "跳到 01:05" }));
    // 只送到嵌入的 origin
    expect(new Set(post.mock.calls.map((c) => c[1]))).toEqual(
      new Set(["https://www.youtube-nocookie.com"]),
    );
    const sent = post.mock.calls.map(
      (c) => JSON.parse(c[0] as string) as { func?: string; args?: unknown[] },
    );
    expect(sent.find((m) => m.func === "seekTo")?.args).toEqual([65, true]);
    expect(screen.getByRole("slider").getAttribute("aria-valuetext")).toBe("播放位置 01:05");
  });

  it("YouTube 沒有回應時，跳播改成更新 start 重新載入", async () => {
    itemApi();
    const { container } = await openItem();
    await userEvent.click(await screen.findByRole("button", { name: "跳到 01:05" }));
    expect(new URL(container.querySelector("iframe")!.src).searchParams.get("start")).toBe("65");
  });

  it("Bilibili 用 t 參數重新載入，並附上在 Bilibili 開啟這個時間", async () => {
    itemApi({ platform: "bilibili" });
    const { container } = await openItem();
    await userEvent.click(await screen.findByRole("button", { name: "跳到 01:05" }));
    const src = new URL(container.querySelector("iframe")!.src);
    expect(src.origin).toBe("https://player.bilibili.com");
    expect(src.searchParams.get("t")).toBe("65");
    expect(
      screen.getByRole("link", { name: "在 Bilibili 開啟 01:05" }).getAttribute("href"),
    ).toContain("t=65");
  });

  it("本機檔案用 <video> 指向 /media；跳播設定 currentTime", async () => {
    itemApi({ platform: "local" });
    const { container } = await openItem();
    const video = container.querySelector("video")!;
    expect(video.getAttribute("src")).toBe("/api/items/item-1/media");
    vi.spyOn(video, "play").mockResolvedValue();
    await userEvent.click(await screen.findByRole("button", { name: "跳到 01:05" }));
    expect(video.currentTime).toBe(65);
  });

  it("媒體無法播放時說明，字幕照常可用", async () => {
    itemApi({ platform: "podcast" });
    const { container } = await openItem();
    fireEvent.error(container.querySelector("audio")!);
    expect(await screen.findByText(/無法播放這個檔案/)).toBeTruthy();
    expect(screen.getByText("比如說 daily stand-up")).toBeTruthy();
  });
});

describe("影音詳情：字幕句列表", () => {
  it("只當純文字渲染、設定字幕的 lang；時間不是來源提供的句子有標示", async () => {
    itemApi({
      variants: {
        source: { lang: "zh-Hans", cues: [...SOURCE, cue(6, 80, 81, "<b>不是粗體</b>")] },
        normalized: { lang: "zh-Hans", cues: SOURCE },
        corrected: { lang: "zh-Hant", cues: CORRECTED },
      },
      stages: DONE_ALL.slice(0, 5),
    });
    const { container } = await openItem("/items/item-1?mode=source");
    const text = await screen.findByText("<b>不是粗體</b>");
    expect(text.getAttribute("lang")).toBe("zh-Hans");
    expect(container.querySelector("b")).toBeNull();
    expect(screen.getByTitle("依停頓推算")).toBeTruthy();
  });

  it("差異模式逐字標出 LLM 改了什麼（合併的句子也對得上）", async () => {
    itemApi();
    const { container } = await openItem("/items/item-1?mode=diff");
    await screen.findByText("刪除的文字");
    const rows = container.querySelectorAll(".cue");
    const first = rows[0]!;
    expect([...first.querySelectorAll("del")].map((e) => e.textContent)).toEqual(["刪除：说"]);
    expect([...first.querySelectorAll("ins")].map((e) => e.textContent)).toEqual(["新增：說 "]);
    // 第 2、3 句與合併後的一句是同一列
    expect(rows[1]!.textContent).toContain("提升");
    expect([...rows[1]!.querySelectorAll("del")].map((e) => e.textContent)).toEqual([
      "刪除：团队",
      "刪除：信息",
    ]);
    // 校正已完成：沒有對應的句子是 LLM 刪掉的，整段顯示成刪除
    expect([...rows[3]!.querySelectorAll("del")].map((e) => e.textContent)).toEqual([
      "刪除：还没校正的句子",
    ]);
    await expectNoA11yViolations(container);
  });

  it("校正完成後 LLM 刪掉的整句顯示成刪除，不是「尚未校正」", async () => {
    itemApi({
      variants: {
        segmented: { lang: "zh-Hans", cues: [cue(1, 0, 2, "嗯"), cue(2, 2, 4, "你好")] },
        corrected: { lang: "zh-Hant", cues: [cue(1, 2, 4, "你好")] },
      },
    });
    const { container } = await openItem("/items/item-1?mode=diff");
    await screen.findByText("刪除的文字");
    expect([...container.querySelectorAll(".cue del")].map((e) => e.textContent)).toEqual([
      "刪除：嗯",
    ]);
    expect(screen.queryByText("尚未校正")).toBeNull();
  });

  it("校正還沒有結果時預設顯示正規化；選「校正」顯示校正前的句子並標「尚未校正」；SSE 不換模式", async () => {
    itemApi({
      stages: [...DONE_ALL.slice(0, 4), stage("correct", "running")],
      variants: {
        normalized: { lang: "zh-Hans", cues: SOURCE },
        segmented: { lang: "zh-Hans", cues: SOURCE },
      },
    });
    const { queryClient } = await openItem();
    await screen.findByText("比如说daily stand-up");
    expect(screen.getByRole("radio", { name: "正規化" }).getAttribute("aria-checked")).toBe("true");
    await act(async () => {
      for (const k of invalidationTable["stage.status"]({
        type: "stage.status",
        item: "yt:x",
        item_id: "item-1",
        stage: "correct",
        status: "done",
      }))
        await queryClient.invalidateQueries({ queryKey: k });
    });
    expect(screen.getByRole("radio", { name: "正規化" }).getAttribute("aria-checked")).toBe("true");
    await userEvent.click(screen.getByRole("radio", { name: "校正" }));
    expect((await screen.findAllByText("尚未校正")).length).toBeGreaterThan(0);
    expect(screen.queryByText("這個版本還沒有字幕。")).toBeNull();
  });

  it("切換模式時捲回同一個時間（對照 → 校正也一樣），不會一直顯示載入中", async () => {
    const scrollTo = vi.fn();
    HTMLElement.prototype.scrollTo = scrollTo as unknown as typeof HTMLElement.prototype.scrollTo;
    // jsdom 的 scrollHeight 是 0，虛擬列表會把每個捲動目標都限制在 0
    const sh = vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(100_000);
    const many = Array.from({ length: 200 }, (_, i) =>
      cue(i + 1, i * 3, i * 3 + 2, `句子 ${i + 1}`),
    );
    itemApi({
      variants: {
        normalized: { lang: "zh-Hans", cues: many },
        corrected: { lang: "zh-Hant", cues: many },
        translated: { lang: "en", cues: many },
        segmented: { lang: "zh-Hans", cues: many },
      },
    });
    const { container } = await openItem();
    await screen.findByText("句子 1");
    const list = container.querySelector<HTMLElement>(".cues__list")!;
    await userEvent.click(screen.getByRole("radio", { name: "對照" }));
    await waitFor(() => expect(container.querySelector(".cue__translated")).toBeTruthy());
    // 在對照模式捲到中間，再切回校正
    list.scrollTop = 64 * 50;
    fireEvent.scroll(list);
    await waitFor(() => expect(screen.queryByText("句子 1")).toBeNull());
    scrollTo.mockClear();
    await userEvent.click(screen.getByRole("radio", { name: "校正" }));
    await waitFor(() => expect(container.querySelector(".cue__translated")).toBeNull());
    // 捲回對照模式第一句（第 51 句）的時間，不是回到頂端
    await waitFor(() =>
      expect(scrollTo.mock.calls.some((c) => (c[0] as { top: number }).top > 0)).toBe(true),
    );
    expect(screen.queryByText("正在載入字幕…")).toBeNull();
    sh.mockRestore();
  });

  it("切換模式寫進網址；對照模式原文與譯文各自設定 lang", async () => {
    itemApi();
    const { router } = await openItem();
    await screen.findByText("比如說 daily stand-up");
    await userEvent.click(screen.getByRole("radio", { name: "對照" }));
    const en = await screen.findByText("For example, the daily stand-up");
    expect(en.getAttribute("lang")).toBe("en");
    expect(screen.getByText("比如說 daily stand-up").getAttribute("lang")).toBe("zh-Hant");
    expect(router.state.location.search).toMatchObject({ mode: "parallel" });
    expect(screen.getByRole("radio", { name: "對照" }).getAttribute("aria-checked")).toBe("true");
  });

  it("沒有完成的步驟不提供那個模式", async () => {
    itemApi({ stages: DONE_ALL.slice(0, 3) });
    await openItem();
    await screen.findByText("比如说daily stand-up");
    const modes = screen.getAllByRole("radio").map((r) => r.textContent);
    expect(modes).toEqual(["原始", "正規化"]);
  });

  it("搜尋只留下符合的句子；Esc 清除", async () => {
    itemApi();
    await openItem();
    await screen.findByText("比如說 daily stand-up");
    const box = screen.getByRole("searchbox", { name: "搜尋字幕" });
    await userEvent.type(box, "六十五");
    expect(await screen.findByText("找到 1 句")).toBeTruthy();
    expect(screen.queryByText("比如說 daily stand-up")).toBeNull();
    await userEvent.keyboard("{Escape}");
    expect(await screen.findByText("比如說 daily stand-up")).toBeTruthy();
  });

  it("有新版本的字幕時才重新讀取（SSE），列表不卸載", async () => {
    const api = itemApi();
    const { queryClient } = await openItem();
    await screen.findByText("比如說 daily stand-up");
    const list = screen.getByRole("list", { name: "字幕句" });
    const before = api.calls("GET", "/api/items/item-1/cues").length;
    const fire = async (status: string) =>
      act(async () => {
        for (const k of invalidationTable["stage.status"]({
          type: "stage.status",
          item: "yt:abcdEFGhijk",
          item_id: "item-1",
          stage: "correct",
          status,
        }))
          await queryClient.invalidateQueries({ queryKey: k });
      });
    await fire("running");
    expect(api.calls("GET", "/api/items/item-1/cues")).toHaveLength(before);
    await fire("done");
    await waitFor(() =>
      expect(api.calls("GET", "/api/items/item-1/cues").length).toBeGreaterThan(before),
    );
    expect(screen.getByRole("list", { name: "字幕句" })).toBe(list);
  });
});

describe("影音詳情：步驟與動作", () => {
  it("失敗的步驟顯示錯誤類別、訊息與動作；API 的 actions 對應到實際的按鈕", async () => {
    const api = itemApi({
      stages: [
        ...DONE_ALL.slice(0, 2),
        stage("normalize", "blocked", {
          block_reason: "needs_action",
          error_class: "needs_action",
          message: "YouTube 要求登入。",
          actions: ["上傳 cookie", "某個新動作"],
        }),
      ],
      routes: { "POST /api/cookies": () => ({ platform: "youtube", cookies: 3, updated_at: "" }) },
    });
    const { container } = await openItem("/items/item-1?tab=stages");
    expect(await screen.findByText("需要你處理：YouTube 要求登入。")).toBeTruthy();
    // 認不得的動作原樣顯示，另外提供設定頁
    expect(screen.getByRole("button", { name: "某個新動作" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "開啟設定" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "從正規化重新處理" })).toBeTruthy();
    await expectNoA11yViolations(container);
    await userEvent.click(screen.getByRole("button", { name: "上傳 cookie" }));
    const dialog = await screen.findByRole("dialog", { name: "上傳 YouTube 的 cookie" });
    await userEvent.type(within(dialog).getByLabelText("或貼上內容"), "SID=abc");
    await userEvent.click(within(dialog).getByRole("button", { name: "上傳 cookie" }));
    await waitFor(() => expect(api.calls("POST", "/api/cookies")).toHaveLength(1));
    expect(JSON.parse(api.calls("POST", "/api/cookies")[0]!.init.body as string)).toEqual({
      platform: "youtube",
      content: "SID=abc",
    });
    expect(await screen.findByText("已儲存 3 個 cookie。")).toBeTruthy();
  });

  it("等待 Agent 的步驟可以改用 API；409 時顯示原因", async () => {
    const api = itemApi({
      stages: [...DONE_ALL.slice(0, 4), stage("correct", "awaiting_agent", { executor: "agent" })],
      routes: {
        "POST /api/items/{id}/switch-executor": () => apiError(409, "api_not_configured", "x"),
      },
    });
    await openItem("/items/item-1?tab=stages");
    await userEvent.click(await screen.findByRole("button", { name: "改用 API 處理" }));
    await waitFor(() =>
      expect(api.calls("POST", "/api/items/item-1/switch-executor")).toHaveLength(1),
    );
    expect(
      JSON.parse(api.calls("POST", "/api/items/item-1/switch-executor")[0]!.init.body as string),
    ).toEqual({
      stage: "correct",
      executor: "api",
    });
    expect(await screen.findByText(/沒有設定 API 的 provider/)).toBeTruthy();
  });

  it("步驟顯示來源紀錄摘要（provider、model、prompt）", async () => {
    itemApi();
    await openItem("/items/item-1?tab=stages");
    expect(await screen.findByText("anthropic / fixture-model")).toBeTruthy();
    expect(screen.getByText("correct-default")).toBeTruthy();
  });

  it("執行紀錄列出每次執行與當時的設定", async () => {
    itemApi();
    await openItem("/items/item-1?tab=runs");
    const list = await screen.findByRole("list", { name: "執行紀錄" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(2);
    expect(within(list).getAllByText("耗時 1m 05s")).toHaveLength(2);
  });

  it("進行中的影音可以停止處理（確認後 POST cancel）", async () => {
    const api = itemApi({
      stages: [...DONE_ALL.slice(0, 4), stage("correct", "running")],
      routes: { "POST /api/items/{id}/cancel": () => detail(item(1), DONE_ALL) },
    });
    await openItem();
    await userEvent.click(screen.getByRole("button", { name: "停止處理…" }));
    const dialog = await screen.findByRole("dialog", { name: "停止處理這部影音？" });
    await userEvent.click(within(dialog).getByRole("button", { name: "停止處理" }));
    await waitFor(() => expect(api.calls("POST", "/api/items/item-1/cancel")).toHaveLength(1));
  });
});

describe("影音詳情：重新處理", () => {
  it("從指定步驟、換模型與 prompt 版本、只重跑指定 chunk", async () => {
    const api = itemApi({
      routes: { "POST /api/items/{id}/rerun": () => detail(item(1), DONE_ALL) },
    });
    await openItem();
    await userEvent.click(screen.getByRole("button", { name: "重新處理…" }));
    const dialog = await screen.findByRole("dialog", { name: "重新處理" });
    await userEvent.selectOptions(within(dialog).getByLabelText("從哪個步驟開始"), "correct");
    await userEvent.type(within(dialog).getByLabelText("Provider"), "openai");
    await userEvent.type(within(dialog).getByLabelText("模型"), "fixture-model");
    await userEvent.selectOptions(
      await within(dialog).findByLabelText("Prompt"),
      "correct-default",
    );
    await userEvent.selectOptions(within(dialog).getByLabelText("Prompt 版本"), "2");
    await userEvent.type(within(dialog).getByLabelText("只重跑這些段落"), "2, 5-6");
    expect(within(dialog).getByText("費用預估：重新處理目前無法預估花費。")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "從校正重新處理" }));
    await waitFor(() => expect(api.calls("POST", "/api/items/item-1/rerun")).toHaveLength(1));
    expect(
      JSON.parse(api.calls("POST", "/api/items/item-1/rerun")[0]!.init.body as string),
    ).toEqual({
      from: "correct",
      overrides: {
        stages: {
          correct: { provider: "openai", model: "fixture-model", prompt: "correct-default@2" },
        },
      },
      chunks: [2, 5, 6],
    });
    expect(await screen.findByText("已從校正重新排入佇列。")).toBeTruthy();
  });

  it("chunks 不存在時（422）指出哪些編號不存在；格式不對時不能送出", async () => {
    itemApi({
      routes: {
        "POST /api/items/{id}/rerun": () =>
          apiError(422, "invalid_request", "x", {
            errors: [{ path: "chunks[1]", message: "chunk 9 不存在；現有的是 1–4。" }],
          }),
      },
    });
    await openItem("/items/item-1?tab=stages");
    await userEvent.click(screen.getByRole("button", { name: "重新處理…" }));
    const dialog = await screen.findByRole("dialog", { name: "重新處理" });
    await userEvent.selectOptions(within(dialog).getByLabelText("從哪個步驟開始"), "translate");
    const chunks = within(dialog).getByLabelText("只重跑這些段落");
    await userEvent.type(chunks, "abc");
    expect(within(dialog).getByText(/段落編號的格式不正確/)).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "從翻譯重新處理" })).toHaveProperty(
      "disabled",
      true,
    );
    await userEvent.clear(chunks);
    await userEvent.type(chunks, "2, 9");
    await userEvent.click(within(dialog).getByRole("button", { name: "從翻譯重新處理" }));
    expect(await within(dialog).findByText("chunk 9 不存在；現有的是 1–4。")).toBeTruthy();
    expect(chunks.getAttribute("aria-invalid")).toBe("true");
  });

  it("其他錯誤（409）用字典或後端訊息顯示；非 LLM 步驟不顯示 chunk 與模型", async () => {
    itemApi({
      routes: { "POST /api/items/{id}/rerun": () => apiError(409, "in_trash", "在垃圾桶中") },
    });
    await openItem();
    await userEvent.click(screen.getByRole("button", { name: "重新處理…" }));
    const dialog = await screen.findByRole("dialog", { name: "重新處理" });
    expect(within(dialog).queryByLabelText("只重跑這些段落")).toBeNull();
    await userEvent.click(within(dialog).getByRole("button", { name: "從字幕重新處理" }));
    expect(await within(dialog).findByText("這部影音在垃圾桶中。還原後就能再使用。")).toBeTruthy();
  });
});

describe("影音詳情：刪除、輸出與匯入", () => {
  it("刪除確認列出影響範圍；勾選刪除輸出檔、改過的檔案保留；完成後回到媒體庫並可復原", async () => {
    const api = itemApi({
      routes: {
        "GET /api/items/{id}/deletion-preview": () => ({
          artifacts: 7,
          bytes: 3 * 1024 * 1024,
          spent_usd: 0.42,
          outputs: [
            { path: "/media/a.srt", modified: false },
            { path: "/media/b.srt", modified: true, reason: "modified" },
          ],
        }),
        "DELETE /api/items/{id}": () => ({
          id: "item-1",
          ref: "yt:x",
          deleted_at: "",
          purge_at: "",
          kept_outputs: [{ path: "/media/b.srt", reason: "modified" }],
        }),
        "POST /api/trash/{id}/restore": () => ({}),
        "GET /api/items": () => ({ items: [], total: 0 }),
      },
    });
    const { router } = await openItem();
    // pages.md §4、DEL 的 @ui 場景：從「更多」選單選「刪除」
    const more = screen.getByRole("button", { name: "更多" });
    expect(more.getAttribute("aria-expanded")).toBe("false");
    await userEvent.click(more);
    expect(more.getAttribute("aria-expanded")).toBe("true");
    await userEvent.click(screen.getByRole("button", { name: "刪除…" }));
    const dialog = await screen.findByRole("dialog", { name: "移到垃圾桶？" });
    expect(await within(dialog).findByText("7 個產出（所有版本）")).toBeTruthy();
    expect(within(dialog).getByText("可釋放 3 MB")).toBeTruthy();
    expect(within(dialog).getByText("已花費 US$0.42（刪除不會退費）")).toBeTruthy();
    expect(within(dialog).getByText("/media/b.srt")).toBeTruthy();
    expect(within(dialog).getByText("已修改，不會刪除")).toBeTruthy();
    const box = within(dialog).getByRole("checkbox", { name: "同時刪除未修改過的輸出檔（1 個）" });
    expect((box as HTMLInputElement).checked).toBe(false);
    await userEvent.click(box);
    expect(within(dialog).getByText("還原時，這些輸出檔會重新寫回原本的位置。")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "移到垃圾桶" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/library"));
    expect(JSON.parse(api.calls("DELETE", "/api/items/item-1")[0]!.init.body as string)).toEqual({
      delete_outputs: true,
    });
    expect(await screen.findByText("已移到垃圾桶。保留了 1 個改過的輸出檔。")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "復原" }));
    await waitFor(() => expect(api.calls("POST", "/api/trash/item-1/restore")).toHaveLength(1));
  });

  it("下載連結與寫出的檔案；匯入字幕的 422 指出第幾句", async () => {
    const api = itemApi({
      routes: {
        "GET /api/items/{id}/outputs": () => ({
          outputs: [
            {
              target: "sidecar",
              format: "srt",
              lang: "zh-Hant",
              path: "/media/v.zh-Hant.srt",
              sha256: "x",
              size: 2048,
              written_at: "2026-11-20T00:00:00Z",
            },
          ],
        }),
        "POST /api/items/{id}/subtitles/import": () =>
          apiError(422, "invalid_request", "時間碼不正確。", { line: 3 }),
      },
    });
    await openItem("/items/item-1?tab=outputs");
    expect(screen.getByRole("link", { name: "WebVTT" }).getAttribute("href")).toBe(
      "/api/items/item-1/subtitles?format=vtt",
    );
    expect(screen.getByRole("link", { name: "雙語 SRT" })).toBeTruthy();
    expect(await screen.findByText("/media/v.zh-Hant.srt")).toBeTruthy();
    expect(screen.getByText("影片旁（sidecar）")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "匯入" }));
    expect(screen.getByText("先選擇一個字幕檔。")).toBeTruthy();
    const file = new File(["1\n00:00:01,000 --> x\n"], "fixed.srt", { type: "text/plain" });
    await userEvent.upload(screen.getByLabelText("字幕檔"), file);
    await userEvent.click(screen.getByRole("button", { name: "匯入" }));
    expect(await screen.findByText("第 3 句：時間碼不正確。")).toBeTruthy();
    const body = JSON.parse(
      api.calls("POST", "/api/items/item-1/subtitles/import")[0]!.init.body as string,
    );
    expect(body).toMatchObject({ name: "fixed.srt", content: "1\n00:00:01,000 --> x\n" });
  });
});

describe("影音詳情：即時宣告與多語系", () => {
  it("這部影音的步驟失敗時由頁面層級的 aria-live 宣告", async () => {
    itemApi();
    await openItem();
    act(() =>
      dispatchServerEvent({
        type: "stage.status",
        item: "yt:x",
        item_id: "item-1",
        stage: "correct",
        status: "failed",
      }),
    );
    expect(await screen.findByText("校正失敗。")).toBeTruthy();
  });

  it("英文介面：標籤換成英文，時間碼不變", async () => {
    itemApi();
    await openItem();
    await act(async () => {
      await i18next.changeLanguage("en");
    });
    expect(await screen.findByRole("radio", { name: "Corrected" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Jump to 01:05" })).toBeTruthy();
    expect(screen.getByRole("slider").getAttribute("aria-valuetext")).toBe(
      "Playback position 00:00",
    );
  });
});
