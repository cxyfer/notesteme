import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes, detail, hubSettings, item, stage } from "../../test/fixtures";
import { apiError, json, mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { hubLink, parseVersionRef } from "./hub";
import { truncateName } from "./HubOrigin";

describe("hub 小工具", () => {
  it("版本 ID 或 Hub 版本頁網址", () => {
    expect(parseVersionRef(" 0193f1a2-aaaa ")).toBe("0193f1a2-aaaa");
    expect(parseVersionRef("https://hub.example.org/versions/v_1?x=1")).toBe("v_1");
    expect(parseVersionRef("https://hub.example.org/videos/youtube/x")).toBeUndefined();
    expect(parseVersionRef("javascript:alert(1)")).toBeUndefined();
  });
  it("只為 https 的 Hub 產生連結", () => {
    expect(hubLink("https://hub.example.org/", "/account")).toBe("https://hub.example.org/account");
    expect(hubLink("javascript:alert(1)", "/account")).toBeUndefined();
    expect(hubLink("http://hub.example.org", "/account")).toBeUndefined();
  });
});

describe("設定「Hub」分區", () => {
  it("URL 唯讀、儲存 token、測試連線、開啟查詢要確認說明（axe 無違規）", async () => {
    let settings = hubSettings({ token_set: false });
    const api = mockApi({
      ...defaultRoutes(),
      "GET /api/settings/hub": () => settings,
      "PUT /api/settings/hub": ({ body }) => {
        const b = body as { token?: string; precheck?: { enabled: boolean } };
        if (b.token !== undefined) settings = hubSettings({ token_set: b.token !== "" });
        if (b.precheck) settings = hubSettings({ enabled: b.precheck.enabled });
        return settings;
      },
      "POST /api/settings/hub/test": () => ({
        display_name: "alice",
        permissions: ["read", "regenerate"],
      }),
    });
    signIn();
    const { container } = renderRoute("/settings/hub");
    expect(await screen.findByText("https://hub.example.org")).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "網址" })).toBeNull();
    expect(screen.getByText(/SHA-256 前 4 個十六進位字元/)).toBeTruthy();
    expect((screen.getByRole("button", { name: "測試連線" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    await userEvent.type(screen.getByLabelText("Hub token"), "nsh_secret");
    await userEvent.click(screen.getByRole("button", { name: "儲存 token" }));
    await screen.findByText("已設定");
    expect(JSON.parse(String(api.calls("PUT", "/api/settings/hub")[0]!.init.body))).toEqual({
      token: "nsh_secret",
    });
    await userEvent.click(screen.getByRole("button", { name: "測試連線" }));
    expect(await screen.findByText("已連線：alice（read, regenerate）")).toBeTruthy();

    const enable = screen.getByRole("button", { name: "開啟查詢" }) as HTMLButtonElement;
    expect(enable.disabled).toBe(true);
    await userEvent.click(screen.getByLabelText("我了解上面送出的內容"));
    await userEvent.click(enable);
    await screen.findByText("已開啟");
    expect(JSON.parse(String(api.calls("PUT", "/api/settings/hub")[1]!.init.body))).toEqual({
      precheck: { enabled: true, acknowledge: "hub-precheck-v2" },
    });
    await expectNoA11yViolations(container);
  });

  it("測試連線失敗時顯示對應的錯誤", async () => {
    mockApi({
      ...defaultRoutes(),
      "POST /api/settings/hub/test": () => apiError(502, "hub_unauthorized", "Hub 拒絕了 token。"),
    });
    signIn();
    renderRoute("/settings/hub");
    await userEvent.click(await screen.findByRole("button", { name: "測試連線" }));
    expect(await screen.findByText("Hub 拒絕了 token。")).toBeTruthy();
  });

  it("用 Hub 的版本重新生成：接受版本頁網址，完成後連到影音；不是原始字幕時顯示 422", async () => {
    let n = 0;
    const api = mockApi({
      ...defaultRoutes(),
      "POST /api/hub/import": () =>
        n++ === 0
          ? json(202, { item: { id: "item-9", ref: "yt:x" } })
          : apiError(422, "not_raw", "只能用別人的原始字幕（source 或 normalized）重新生成。"),
    });
    signIn();
    renderRoute("/settings/hub");
    const input = await screen.findByLabelText("版本網址或 ID");
    await userEvent.type(input, "https://hub.example.org/versions/v1");
    await userEvent.click(screen.getByRole("button", { name: "開始處理" }));
    expect((await screen.findByRole("link", { name: "開啟影音" })).getAttribute("href")).toBe(
      "/items/item-9",
    );
    expect(JSON.parse(String(api.calls("POST", "/api/hub/import")[0]!.init.body))).toEqual({
      version: "v1",
    });
    await userEvent.type(input, "v2");
    await userEvent.click(screen.getByRole("button", { name: "開始處理" }));
    expect(await screen.findByText(/只能用別人的原始字幕/)).toBeTruthy();
  });
});

function itemWithShares() {
  return {
    ...detail(item(1, { original_lang: "en" }), [
      stage("metadata", "done"),
      stage("transcript", "done", { executor: "hub" }),
      stage("normalize", "done"),
      stage("correct", "done"),
    ]),
    sources: [{ kind: "manual", lang: "en", source: "hub", state: "used", reason: "" }],
    hub_shares: [
      { share_id: "s1", hub_url: "https://hub.example.org", created_at: "2026-11-23T00:00:00Z" },
    ],
  };
}

describe("影音詳情：分享到 Hub", () => {
  it("標示 Hub 來源、列出已分享；對話框有產出、授權、聲明與預覽，送出白名單的參數（axe 無違規）", async () => {
    const api = mockApi({
      ...defaultRoutes(),
      "GET /api/items/{id}": () => itemWithShares(),
      "POST /api/items/{id}/hub/share": () =>
        json(201, {
          share_id: "s2",
          versions: [
            {
              kind: "transcript",
              variant: "corrected",
              lang: "en",
              version_id: "v9",
              deduplicated: false,
            },
          ],
        }),
    });
    signIn();
    renderRoute("/items/item-1");
    expect(await screen.findByText("來自 Hub 的原始字幕")).toBeTruthy();
    const shared = screen.getByRole("region", { name: "已分享到 Hub" });
    expect(within(shared).getByRole("link").getAttribute("href")).toBe(
      "https://hub.example.org/account",
    );
    expect(within(shared).getByRole("link").getAttribute("rel")).toBe("noopener noreferrer");

    await userEvent.click(screen.getByRole("button", { name: "分享到 Hub" }));
    const dialog = await screen.findByRole("dialog", { name: "分享到 Hub" });
    const submit = within(dialog).getByRole("button", { name: "分享到 Hub" }) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    await userEvent.click(within(dialog).getByText("預覽會送出的影音欄位"));
    expect(within(dialog).getByText("影片 1")).toBeTruthy();
    await userEvent.click(within(dialog).getByLabelText("只分享勾選的"));
    const corrected = within(dialog).getByLabelText("transcript · corrected · en");
    expect(within(dialog).getByLabelText("transcript · source · en")).toBeTruthy();
    await userEvent.click(within(dialog).getByLabelText("transcript · source · en"));
    await userEvent.click(within(dialog).getByLabelText("transcript · normalized · en"));
    // 替身的 cues 對任何變體都有內容，所以也列出了 edited
    await userEvent.click(within(dialog).getByLabelText("transcript · edited · en"));
    expect((corrected as HTMLInputElement).checked).toBe(true);
    await userEvent.selectOptions(within(dialog).getByLabelText("授權"), "CC-BY-SA-4.0");
    await userEvent.click(within(dialog).getByLabelText("我確認有權分享這些內容"));
    await expectNoA11yViolations(dialog);
    await userEvent.click(submit);
    const link = await within(dialog).findByRole("link", { name: "transcript · corrected · en" });
    expect(link.getAttribute("href")).toBe("https://hub.example.org/versions/v9");
    expect(
      JSON.parse(String(api.calls("POST", "/api/items/{id}/hub/share")[0]!.init.body)),
    ).toEqual({
      license: "CC-BY-SA-4.0",
      attest: true,
      artifacts: [{ kind: "transcript", variant: "corrected", lang: "en" }],
    });
  });

  it("沒有設定 Hub 時提供「設定 Hub」", async () => {
    mockApi({
      ...defaultRoutes(),
      "GET /api/items/{id}": () => itemWithShares(),
      "GET /api/settings/hub": () => hubSettings({ token_set: false }),
      "POST /api/items/{id}/hub/share": () =>
        apiError(409, "hub_not_configured", "尚未設定 Hub。", { actions: ["設定 Hub"] }),
    });
    signIn();
    const { router } = renderRoute("/items/item-1");
    await userEvent.click(await screen.findByRole("button", { name: "分享到 Hub" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.selectOptions(within(dialog).getByLabelText("授權"), "CC0-1.0");
    await userEvent.click(within(dialog).getByLabelText("我確認有權分享這些內容"));
    await userEvent.click(within(dialog).getByRole("button", { name: "分享到 Hub" }));
    await userEvent.click(await within(dialog).findByRole("button", { name: "設定 Hub" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/hub"));
  });

  it("本機檔案沒有分享按鈕", async () => {
    mockApi({
      ...defaultRoutes(),
      "GET /api/items/{id}": () =>
        detail(item(1, { platform: "local", url: "" }), [stage("transcript", "done")]),
    });
    signIn();
    renderRoute("/items/item-1");
    await screen.findByRole("heading", { level: 1 });
    expect(screen.queryByRole("button", { name: "分享到 Hub" })).toBeNull();
  });
});

describe("影音詳情：來自誰的 Hub 字幕（HSHR S23）", () => {
  const origin = (over: Record<string, string> = {}) => ({
    url: "https://hub.example.org",
    version_id: "v_42",
    sha256: "a".repeat(64),
    uploader: "alice",
    ...over,
  });
  const openWith = async (hubOrigin: ReturnType<typeof origin> | null) => {
    mockApi({
      ...defaultRoutes(),
      "GET /api/items/{id}": () => ({ ...itemWithShares(), hub_origin: hubOrigin }),
    });
    signIn();
    const r = renderRoute("/items/item-1");
    await screen.findByRole("heading", { level: 1 });
    return r;
  };

  it("顯示「來自 Hub：@alice 的原始字幕」與 Hub 版本頁的連結（axe 無違規）", async () => {
    const { container } = await openWith(origin());
    expect(await screen.findByText("來自 Hub：@alice 的原始字幕")).toBeTruthy();
    const link = screen.getByRole("link", { name: "在 Hub 查看這個版本" });
    expect(link.getAttribute("href")).toBe("https://hub.example.org/versions/v_42");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
    expect(link.getAttribute("target")).toBe("_blank");
    await expectNoA11yViolations(container);
  });

  it("上傳者名稱是不可信的文字：不當成 HTML，過長時截斷並在 title 放完整名稱", async () => {
    const evil = '<img src=x onerror="alert(1)">' + "很長的名字".repeat(10);
    const { container } = await openWith(origin({ uploader: evil }));
    const text = await screen.findByText(/^來自 Hub：@<img src=x/);
    expect(container.querySelector("img[src=x]")).toBeNull();
    expect(text.textContent).toBe(
      `來自 Hub：@${Array.from(evil).slice(0, 32).join("")}… 的原始字幕`,
    );
    expect(text.getAttribute("title")).toBe(evil);
  });

  it("Hub 網址不是 https 時不產生連結；沒有上傳者名稱時用通用的說法", async () => {
    await openWith(origin({ url: "javascript:alert(1)", uploader: "" }));
    expect(await screen.findByText("來自 Hub 的原始字幕")).toBeTruthy();
    expect(screen.queryByRole("link", { name: "在 Hub 查看這個版本" })).toBeNull();
  });

  it("http 的 Hub 也不產生連結（沿用「已分享到 Hub」的規則）", async () => {
    await openWith(origin({ url: "http://hub.example.org" }));
    expect(await screen.findByText("來自 Hub：@alice 的原始字幕")).toBeTruthy();
    expect(screen.queryByRole("link", { name: "在 Hub 查看這個版本" })).toBeNull();
  });

  it("hub_origin 是 null 時維持原本的「來自 Hub 的原始字幕」", async () => {
    await openWith(null);
    expect(await screen.findByText("來自 Hub 的原始字幕")).toBeTruthy();
    expect(screen.queryByText(/來自 Hub：@/)).toBeNull();
  });
});

describe("truncateName", () => {
  it("以 code point 計，不切開 surrogate pair", () => {
    expect(truncateName("alice")).toBe("alice");
    expect(truncateName("😀".repeat(40), 3)).toBe("😀😀😀…");
  });
});
