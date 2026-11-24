import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../test/axe";
import { item } from "../test/fixtures";
import { apiError, json, mockApi, type MockHandler } from "../test/mockApi";
import { renderRoute, signIn } from "../test/render";

const PROFILES = {
  profiles: [
    {
      name: "default",
      version: 1,
      config: {},
      builtin: true,
      default: true,
      created_at: "",
      updated_at: "",
    },
    {
      name: "notes",
      version: 2,
      config: {},
      builtin: false,
      default: false,
      created_at: "",
      updated_at: "",
    },
  ],
};

function ingested(n: number, over = {}) {
  return { ...item(n), already_in_library: false, in_trash: false, ...over };
}

const YT = "https://www.youtube.com/watch?v=vid00000001";

function drawerApi(routes: Record<string, MockHandler> = {}) {
  return mockApi({
    "GET /api/items": () => ({ items: [], total: 0 }),
    "GET /api/profiles": () => PROFILES,
    "POST /api/resolve": (req) => {
      const url = (req.body as { url: string }).url;
      if (url.includes("youtube.com/watch"))
        return {
          platform: "youtube",
          kind: "video",
          url,
          external_id: "vid00000001",
          subscribable: false,
          requires_cookie: false,
        };
      if (url.includes("bilibili.com/video"))
        return {
          platform: "bilibili",
          kind: "video",
          url,
          external_id: "BV1xx411c7mD",
          subscribable: false,
          requires_cookie: false,
        };
      if (url.includes("/@"))
        return {
          platform: "youtube",
          kind: "channel",
          url,
          subscribable: true,
          requires_cookie: false,
        };
      if (url.endsWith("feed.xml"))
        return {
          platform: "podcast",
          kind: "feed",
          url,
          subscribable: false,
          requires_cookie: false,
          episodes: [
            { guid: "ep-1", title: "第一集", has_transcript: false, duration_s: 1800 },
            { guid: "ep-2", title: "第二集", has_transcript: true },
          ],
        };
      return apiError(422, "unrecognized_url", "無法辨識（後端）");
    },
    "POST /api/items": () => json(201, { items: [ingested(1)] }),
    "POST /api/estimate": () => ESTIMATE,
    ...routes,
  });
}

const ESTIMATE = {
  items: [
    {
      ref: "yt:vid00000001",
      title: "",
      duration_s: 600,
      expected_source: "youtube-auto",
      asr_usd: 0,
      source_usd: null,
      llm_usd: 0.21,
      total_usd: 0.21,
      requires_cookie: false,
      already_in_library: false,
      notes: ["有自動字幕，可能用不到語音轉錄。"],
    },
  ],
  total_usd: 0.21,
  currency: "USD",
  prices_as_of: "2026-11-01",
  notes: [],
};

async function openDrawer(path = "/library") {
  signIn();
  const r = renderRoute(path);
  await screen.findByRole("heading", { level: 1, name: "媒體庫" });
  act(() => (document.activeElement as HTMLElement | null)?.blur());
  fireEvent.keyDown(document.body, { key: "n" });
  const dialog = await screen.findByRole("dialog", { name: "新增" });
  return { ...r, dialog };
}

function paste(target: EventTarget, text: string) {
  const ev = new Event("paste", { bubbles: true, cancelable: true }) as Event & {
    clipboardData: { getData: (t: string) => string };
  };
  ev.clipboardData = { getData: (t) => (t === "text/plain" ? text : "") };
  act(() => {
    target.dispatchEvent(ev);
  });
}

describe("新增抽屜", () => {
  it("N 打開；貼上 YouTube 網址 → 預覽 → 開始處理 → toast 並關閉抽屜", async () => {
    const api = drawerApi();
    const { dialog, router } = await openDrawer();
    await userEvent.type(within(dialog).getByLabelText("網址"), YT);
    expect(await within(dialog).findByRole("heading", { name: "YouTube 影片" })).toBeTruthy();
    // 處理方案預設選中 default
    await waitFor(() =>
      expect((within(dialog).getByLabelText("處理方案") as HTMLSelectElement).value).toBe(
        "default",
      ),
    );
    await userEvent.selectOptions(within(dialog).getByLabelText("處理方案"), "notes");
    await userEvent.click(within(dialog).getByRole("button", { name: "開始處理" }));
    await waitFor(() => expect(api.calls("POST", "/api/items")).toHaveLength(1));
    expect(JSON.parse(String(api.calls("POST", "/api/items")[0]!.init.body))).toEqual({
      url: YT,
      profile: "notes",
    });
    expect(await screen.findByText("已開始處理 1 部")).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    // 「查看」導向新的影音
    await userEvent.click(screen.getByRole("button", { name: "查看" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/items/item-1"));
  });

  it("焦點不在輸入框時貼上網址，直接打開抽屜並帶入（README §3.2）", async () => {
    const api = drawerApi();
    signIn();
    renderRoute("/library");
    await screen.findByRole("heading", { level: 1, name: "媒體庫" });
    paste(document.body, "not a url");
    expect(screen.queryByRole("dialog")).toBeNull();
    paste(document.body, YT);
    const dialog = await screen.findByRole("dialog", { name: "新增" });
    expect((within(dialog).getByLabelText("網址") as HTMLInputElement).value).toBe(YT);
    await waitFor(() => expect(api.calls("POST", "/api/resolve")).toHaveLength(1));
  });

  it("焦點在輸入框時貼上不會打開抽屜", async () => {
    drawerApi();
    signIn();
    renderRoute("/library");
    const box = await screen.findByLabelText("搜尋標題");
    paste(box, YT);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("辨識失敗依 code 顯示前端的文案，並提供修改網址", async () => {
    drawerApi();
    const { dialog } = await openDrawer();
    const input = within(dialog).getByLabelText("網址");
    await userEvent.type(input, "https://example.com/page");
    expect(
      await within(dialog).findByText(
        "無法辨識這個網址。目前支援 YouTube、Bilibili、Podcast RSS 與本機檔案。",
      ),
    ).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "開始處理" })).toHaveProperty(
      "disabled",
      true,
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "修改網址" }));
    expect(document.activeElement).toBe(input);
  });

  it("已在媒體庫中：留在抽屜並提供「打開」（ING-R2）", async () => {
    drawerApi({
      "POST /api/items": () => json(200, { items: [ingested(1, { already_in_library: true })] }),
    });
    const { dialog, router } = await openDrawer();
    await userEvent.type(within(dialog).getByLabelText("網址"), YT);
    await within(dialog).findByRole("heading", { name: "YouTube 影片" });
    await userEvent.click(within(dialog).getByRole("button", { name: "開始處理" }));
    expect(await within(dialog).findByText("已在媒體庫中（1 部），沒有重複新增。")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "打開" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/items/item-1"));
  });

  it("在垃圾桶中（409 in_trash）：提供「從垃圾桶還原」", async () => {
    const api = drawerApi({
      "POST /api/items": () => apiError(409, "in_trash", "在垃圾桶中", { item_id: "item-9" }),
      "POST /api/trash/{id}/restore": () => ({}),
    });
    const { dialog } = await openDrawer();
    await userEvent.type(within(dialog).getByLabelText("網址"), YT);
    await within(dialog).findByRole("heading", { name: "YouTube 影片" });
    await userEvent.click(within(dialog).getByRole("button", { name: "開始處理" }));
    expect(await within(dialog).findByText("這部影音在垃圾桶中。還原後就能再使用。")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "從垃圾桶還原" }));
    await waitFor(() => expect(api.calls("POST", "/api/trash/{id}/restore")).toHaveLength(1));
    expect(api.calls("POST", "/api/trash/{id}/restore")[0]!.url.pathname).toBe(
      "/api/trash/item-9/restore",
    );
    expect(await screen.findByText("已從垃圾桶還原")).toBeTruthy();
  });

  it("Bilibili 沒有指定分P：可以只要指定的分P；格式不對時不能送出", async () => {
    const api = drawerApi();
    const { dialog } = await openDrawer();
    await userEvent.type(
      within(dialog).getByLabelText("網址"),
      "https://www.bilibili.com/video/BV1xx411c7mD",
    );
    await within(dialog).findByRole("heading", { name: "Bilibili 影片" });
    await userEvent.click(within(dialog).getByLabelText("只要指定的分P"));
    const parts = within(dialog).getByLabelText("分P 編號");
    await userEvent.type(parts, "x");
    expect(within(dialog).getByRole("button", { name: "開始處理" })).toHaveProperty(
      "disabled",
      true,
    );
    await userEvent.clear(parts);
    await userEvent.type(parts, "1, 3");
    await userEvent.click(within(dialog).getByRole("button", { name: "開始處理" }));
    await waitFor(() => expect(api.calls("POST", "/api/items")).toHaveLength(1));
    expect(JSON.parse(String(api.calls("POST", "/api/items")[0]!.init.body)).parts).toEqual([1, 3]);
  });

  it("Podcast feed 要勾選集數才能送出（ING-R6）", async () => {
    const api = drawerApi();
    const { dialog } = await openDrawer();
    await userEvent.type(within(dialog).getByLabelText("網址"), "https://example.com/feed.xml");
    await within(dialog).findByRole("heading", { name: "Podcast feed" });
    const submit = within(dialog).getByRole("button", { name: "開始處理" });
    expect(submit).toHaveProperty("disabled", true);
    expect(within(dialog).getByText("勾選至少一集。")).toBeTruthy();
    await userEvent.click(within(dialog).getByLabelText(/第二集/));
    await userEvent.click(submit);
    await waitFor(() => expect(api.calls("POST", "/api/items")).toHaveLength(1));
    expect(JSON.parse(String(api.calls("POST", "/api/items")[0]!.init.body)).episodes).toEqual([
      "ep-2",
    ]);
  });

  it("辨識成功後顯示預估花費（ENTRY 的書籤場景）；估算失敗不擋開始處理", async () => {
    let fail = false;
    const api = drawerApi({
      "POST /api/estimate": () =>
        fail ? apiError(429, "rate_limited", "請求太頻繁，請稍後再試。") : ESTIMATE,
    });
    const { dialog } = await openDrawer();
    await userEvent.type(within(dialog).getByLabelText("網址"), YT);
    expect(await within(dialog).findByText("預估花費約 US$0.21。")).toBeTruthy();
    expect(within(dialog).getByText("有自動字幕，可能用不到語音轉錄。")).toBeTruthy();
    const body = JSON.parse(String(api.calls("POST", "/api/estimate")[0]!.init.body));
    expect(body).toEqual({ url: YT, profile: "default" });
    // 換處理方案會重新估算；這次失敗：寫明原因，但仍然可以開始處理
    fail = true;
    await userEvent.selectOptions(within(dialog).getByLabelText("處理方案"), "notes");
    expect(await within(dialog).findByText(/無法預估花費：/)).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "開始處理" })).toHaveProperty(
      "disabled",
      false,
    );
    expect(api.calls("POST", "/api/items")).toHaveLength(0);
  });

  it("全部金額未知時寫「無法預估花費」，不顯示 $0.00", async () => {
    drawerApi({
      "POST /api/estimate": () => ({
        ...ESTIMATE,
        items: [{ ...ESTIMATE.items[0], llm_usd: null, total_usd: null, notes: [] }],
        total_usd: 0,
      }),
    });
    const { dialog } = await openDrawer();
    await userEvent.type(within(dialog).getByLabelText("網址"), YT);
    expect(await within(dialog).findByText("無法預估花費。")).toBeTruthy();
    expect(within(dialog).queryByText(/\$0\.00/)).toBeNull();
  });

  it("貼上頻道：提供「建立訂閱」，打開新增訂閱並帶入網址與一台實例的提醒（SUB 的 @ui 場景）", async () => {
    const api = drawerApi({ "GET /api/subscriptions": () => ({ subscriptions: [] }) });
    const { dialog, router } = await openDrawer();
    const channel = "https://www.youtube.com/@fixturechannel";
    await userEvent.type(within(dialog).getByLabelText("網址"), channel);
    await within(dialog).findByRole("heading", { name: "YouTube 頻道" });
    await userEvent.click(within(dialog).getByRole("button", { name: "建立訂閱" }));
    // 頻道不估價
    expect(api.calls("POST", "/api/estimate")).toHaveLength(0);
    await waitFor(() => expect(router.state.location.pathname).toBe("/subscriptions"));
    const drawer = await screen.findByRole("dialog", { name: "新增訂閱" });
    expect(within(drawer).getByText("同一個頻道只在一台實例上訂閱，避免重複處理。")).toBeTruthy();
    expect((within(drawer).getByLabelText("來源") as HTMLInputElement).value).toBe(channel);
  });

  it("頻道不能直接新增：提示訂閱並停用送出（ING-R5）", async () => {
    drawerApi();
    const { dialog } = await openDrawer();
    await userEvent.type(within(dialog).getByLabelText("網址"), "https://www.youtube.com/@someone");
    await within(dialog).findByRole("heading", { name: "YouTube 頻道" });
    expect(within(dialog).getByRole("button", { name: "開始處理" })).toHaveProperty(
      "disabled",
      true,
    );
    expect(
      within(dialog).getAllByText(/這是頻道。要追蹤新內容，請建立訂閱/).length,
    ).toBeGreaterThan(0);
  });

  it("選擇本機檔案：multipart 上傳 file 與 profile；檔案太大時提供選擇其他檔案", async () => {
    let first = true;
    const api = drawerApi({
      "POST /api/items": () => {
        if (first) {
          first = false;
          return apiError(413, "upload_too_large", "太大");
        }
        return json(201, { items: [ingested(2)] });
      },
    });
    const { dialog } = await openDrawer();
    const file = new File(["data"], "talk.mp4", { type: "video/mp4" });
    const input = dialog.querySelector<HTMLInputElement>("input[type=file]")!;
    await userEvent.upload(input, file);
    expect(within(dialog).getByText("已選擇 talk.mp4")).toBeTruthy();
    expect(within(dialog).getByLabelText("網址")).toHaveProperty("disabled", true);
    await waitFor(() =>
      expect((within(dialog).getByLabelText("處理方案") as HTMLSelectElement).value).toBe(
        "default",
      ),
    );
    const submit = within(dialog).getByRole("button", { name: "開始處理" });
    await userEvent.click(submit);
    expect(await within(dialog).findByText("檔案超過上傳大小上限。")).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "選擇其他檔案" })).toBeTruthy();
    const body = api.calls("POST", "/api/items")[0]!.init.body as FormData;
    expect(body).toBeInstanceOf(FormData);
    expect((body.get("file") as File).name).toBe("talk.mp4");
    expect(body.get("profile")).toBe("default");
    expect([...body.keys()]).toEqual(["profile", "file"]);
    await userEvent.click(submit);
    expect(await screen.findByText("已開始處理 1 部")).toBeTruthy();
  });

  it("把檔案拖進視窗：打開抽屜並帶入第一個檔案，多個時提示；拖文字不接手（pages.md §2）", async () => {
    drawerApi();
    signIn();
    renderRoute("/library");
    await screen.findByRole("heading", { level: 1, name: "媒體庫" });
    const a = new File(["a"], "talk.mp4", { type: "video/mp4" });
    const b = new File(["b"], "other.mp4", { type: "video/mp4" });

    // 拖的是文字：不 preventDefault、不打開
    const textOver = fireEvent.dragOver(document.body, { dataTransfer: { types: ["text/plain"] } });
    expect(textOver).toBe(true);
    fireEvent.drop(document.body, { dataTransfer: { types: ["text/plain"], files: [] } });
    expect(screen.queryByRole("dialog")).toBeNull();

    // 拖的是檔案：dragover 要 preventDefault 才能 drop
    const fileOver = fireEvent.dragOver(document.body, { dataTransfer: { types: ["Files"] } });
    expect(fileOver).toBe(false);
    const dropped = fireEvent.drop(document.body, {
      dataTransfer: { types: ["Files"], files: [a, b] },
    });
    expect(dropped).toBe(false);
    const dialog = await screen.findByRole("dialog", { name: "新增" });
    expect(within(dialog).getByText("已選擇 talk.mp4")).toBeTruthy();
    expect(
      within(dialog).getByText("一次只能上傳一個檔案。拖進來的 2 個檔案只帶入了第一個：talk.mp4。"),
    ).toBeTruthy();
    expect(within(dialog).getByLabelText("網址")).toHaveProperty("disabled", true);
  });

  it("調整這次的設定：預設收合；只送有改的欄位，預估與新增都帶 overrides（ING-R7）", async () => {
    const api = drawerApi({
      "GET /api/settings/{section}": (req) =>
        req.params.section === "language"
          ? { targets: ["zh-Hant-TW"], fallback: ["supadata", "groq"] }
          : {
              correct: { executor: "api" },
              translate: { executor: "api" },
              frames: { executor: "off" },
            },
    });
    const { dialog } = await openDrawer();
    const toggle = within(dialog).getByRole("button", { name: "調整這次的設定" });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(within(dialog).queryByRole("textbox", { name: "目標語言" })).toBeNull();
    // 收合時不讀設定
    expect(api.calls("GET", "/api/settings/{section}")).toHaveLength(0);

    await userEvent.type(within(dialog).getByLabelText("網址"), YT);
    expect(await within(dialog).findByText("預估花費約 US$0.21。")).toBeTruthy();
    await userEvent.click(toggle);
    const targets = within(dialog).getByLabelText("目標語言");
    expect(await within(dialog).findByText(/目前是 zh-Hant-TW/)).toBeTruthy();
    // 字幕來源：順序來自全域設定，可以調整
    const sources = within(dialog).getByRole("group", { name: "字幕來源" });
    expect(
      within(sources)
        .getAllByRole("checkbox")
        .map((c) => c.parentElement!.textContent),
    ).toEqual(["Supadata", "Groq 語音轉錄"]);
    await userEvent.click(within(sources).getByRole("button", { name: "把Groq 語音轉錄往前移" }));
    // 步驟：處理方案沒有開啟的不能打開
    const stages = within(dialog).getByRole("group", { name: "步驟" });
    expect(within(stages).getByRole("checkbox", { name: /關鍵畫面/ })).toHaveProperty(
      "disabled",
      true,
    );
    await userEvent.click(within(stages).getByRole("checkbox", { name: "翻譯" }));
    await userEvent.type(targets, "ja, en");

    expect(within(dialog).getByRole("button", { name: "調整這次的設定（已調整）" })).toBeTruthy();
    const want = {
      language: { targets: ["ja", "en"], fallback: ["groq", "supadata"] },
      stages: { translate: { executor: "off" } },
    };
    await waitFor(() =>
      expect(
        JSON.parse(String(api.calls("POST", "/api/estimate").at(-1)!.init.body)).overrides,
      ).toEqual(want),
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "開始處理" }));
    await waitFor(() => expect(api.calls("POST", "/api/items")).toHaveLength(1));
    expect(JSON.parse(String(api.calls("POST", "/api/items")[0]!.init.body))).toEqual({
      url: YT,
      profile: "default",
      overrides: want,
    });
  });

  it("調整這次的設定：讀不到全域設定時不提供調整字幕來源；422 顯示在欄位旁", async () => {
    const api = drawerApi({
      "GET /api/settings/{section}": () => apiError(403, "forbidden", "權限不足。"),
      "POST /api/items": () =>
        apiError(422, "invalid_input", "language.targets.0：不是合法的語言標籤。", {
          errors: [{ path: "language.targets.0", message: "不是合法的語言標籤。" }],
        }),
    });
    const { dialog } = await openDrawer();
    await userEvent.type(within(dialog).getByLabelText("網址"), YT);
    await within(dialog).findByRole("heading", { name: "YouTube 影片" });
    await userEvent.click(within(dialog).getByRole("button", { name: "調整這次的設定" }));
    expect(
      await within(dialog).findByText("讀不到全域的字幕來源設定，這次不能調整字幕來源。"),
    ).toBeTruthy();
    await userEvent.type(within(dialog).getByLabelText("目標語言"), "??");
    await userEvent.click(within(dialog).getByRole("button", { name: "開始處理" }));
    expect(await within(dialog).findByText("不是合法的語言標籤。")).toBeTruthy();
    expect(within(dialog).getByLabelText("目標語言").getAttribute("aria-invalid")).toBe("true");
    expect(JSON.parse(String(api.calls("POST", "/api/items")[0]!.init.body)).overrides).toEqual({
      language: { targets: ["??"] },
    });
  });

  it("選了本機檔案時，單次覆寫放在 multipart 的 overrides 欄位（在 file 之前）", async () => {
    const api = drawerApi({
      "GET /api/settings/{section}": () => ({ fallback: ["groq"] }),
      "POST /api/items": () => json(201, { items: [ingested(2)] }),
    });
    const { dialog } = await openDrawer();
    await userEvent.upload(
      dialog.querySelector<HTMLInputElement>("input[type=file]")!,
      new File(["data"], "talk.mp4", { type: "video/mp4" }),
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "調整這次的設定" }));
    await userEvent.click(within(dialog).getByRole("checkbox", { name: "筆記" }));
    await waitFor(() =>
      expect((within(dialog).getByLabelText("處理方案") as HTMLSelectElement).value).toBe(
        "default",
      ),
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "開始處理" }));
    await waitFor(() => expect(api.calls("POST", "/api/items")).toHaveLength(1));
    const body = api.calls("POST", "/api/items")[0]!.init.body as FormData;
    expect([...body.keys()]).toEqual(["profile", "overrides", "file"]);
    expect(JSON.parse(body.get("overrides") as string)).toEqual({
      stages: { note: { executor: "off" } },
    });
  });

  it("一行一個網址：逐行辨識與預估，無效的行就地標示不擋其他行，逐個新增並合計（axe 無違規）", async () => {
    const YT2 = "https://www.youtube.com/watch?v=vid00000002";
    const BILI = "https://www.bilibili.com/video/BV1xx411c7mD";
    let inFlight = 0;
    let maxInFlight = 0;
    const api = drawerApi({
      "POST /api/estimate": async () => {
        inFlight++;
        maxInFlight = Math.max(maxInFlight, inFlight);
        await new Promise((r) => setTimeout(r, 5));
        inFlight--;
        return ESTIMATE;
      },
      "POST /api/items": (req) => {
        const url = (req.body as { url: string }).url;
        if (url === YT2) return json(200, { items: [ingested(2, { already_in_library: true })] });
        return json(201, { items: [ingested(url === YT ? 1 : 3)] });
      },
    });
    const { dialog } = await openDrawer();
    const box = within(dialog).getByLabelText("網址");
    // 重複的行與空行略過
    fireEvent.change(box, {
      target: {
        value: `${YT}\n\nnot a url\n${YT2}\n${YT}\nhttps://www.youtube.com/@fixturechannel\n${BILI}`,
      },
    });
    const lines = await within(dialog).findByRole("region", { name: "5 個網址" });
    expect(await within(lines).findByText("請輸入 http 或 https 開頭的完整網址。")).toBeTruthy();
    expect(
      await within(lines).findByText("這是頻道，不能直接新增。請一次貼一個網址，再建立訂閱。"),
    ).toBeTruthy();
    expect(await within(lines).findByText(/全部分P/)).toBeTruthy();
    // 三行可以新增，預估依序送出（一次一個），合計 3 × 0.21
    expect(await within(lines).findByText("預估花費約 US$0.63。")).toBeTruthy();
    expect(api.calls("POST", "/api/estimate")).toHaveLength(3);
    expect(maxInFlight).toBe(1);
    await expectNoA11yViolations(document.body);

    await userEvent.click(within(dialog).getByRole("button", { name: "開始處理" }));
    await waitFor(() => expect(api.calls("POST", "/api/items")).toHaveLength(3));
    expect(api.calls("POST", "/api/items").map((c) => JSON.parse(String(c.init.body)).url)).toEqual(
      [YT, YT2, BILI],
    );
    expect(await screen.findByText("已開始處理 2 部，1 部已在媒體庫中")).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("一行一個網址：部分失敗時留在抽屜，成功的行移除、失敗的行標示原因", async () => {
    const YT2 = "https://www.youtube.com/watch?v=vid00000002";
    const api = drawerApi({
      "POST /api/items": (req) =>
        (req.body as { url: string }).url === YT2
          ? apiError(409, "in_trash", "在垃圾桶中", { item_id: "item-9" })
          : json(201, { items: [ingested(1)] }),
    });
    const { dialog } = await openDrawer();
    fireEvent.change(within(dialog).getByLabelText("網址"), {
      target: { value: `${YT}\n${YT2}\nnot a url` },
    });
    const lines = await within(dialog).findByRole("region", { name: "3 個網址" });
    await within(lines).findByText("預估花費約 US$0.42。");
    await userEvent.click(within(dialog).getByRole("button", { name: "開始處理" }));
    expect(await screen.findByText("已開始處理 1 部，1 個網址沒有新增")).toBeTruthy();
    expect(api.calls("POST", "/api/items")).toHaveLength(2);
    expect(screen.getByRole("dialog", { name: "新增" })).toBeTruthy();
    expect((within(dialog).getByLabelText("網址") as HTMLTextAreaElement).value).toBe(
      `${YT2}\nnot a url`,
    );
    expect(await within(dialog).findByText("這部影音在垃圾桶中。還原後就能再使用。")).toBeTruthy();
  });

  it("一行一個網址：超過 20 行時不能送出", async () => {
    drawerApi();
    const { dialog } = await openDrawer();
    const many = Array.from({ length: 21 }, (_, i) => `https://www.youtube.com/watch?v=v${i}`);
    fireEvent.change(within(dialog).getByLabelText("網址"), { target: { value: many.join("\n") } });
    expect(
      (await within(dialog).findAllByText("一次最多 20 個網址，多了 1 個。請分次新增。")).length,
    ).toBeGreaterThan(0);
    expect(within(dialog).getByRole("button", { name: "開始處理" })).toHaveProperty(
      "disabled",
      true,
    );
  });

  it("打開時沒有 axe 問題；Esc 關閉後焦點回到開啟前的元素", async () => {
    drawerApi();
    signIn();
    renderRoute("/library");
    const addButton = (await screen.findAllByRole("button", { name: "新增" }))[0]!;
    await userEvent.click(addButton);
    const dialog = await screen.findByRole("dialog", { name: "新增" });
    await userEvent.type(within(dialog).getByLabelText("網址"), "https://example.com/feed.xml");
    await within(dialog).findByRole("heading", { name: "Podcast feed" });
    await expectNoA11yViolations(document.body);
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(addButton);
  });
});
