import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { notifier } from "../../test/m4Fixtures";
import { apiError, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { SETTINGS_SECTIONS } from "../SettingsPage";

const SECRET_KEY = "sk-test-SECRET-abcd1234";
const TOKEN = "nst_PLAINTEXT_TOKEN_value";

const SYSTEM = {
  listen: "127.0.0.1:8080",
  listen_source: "default",
  data: "/data",
  data_source: "env",
  tools: {
    "yt-dlp": { path: "/data/tools/yt-dlp", source: "managed", available: true },
    ffmpeg: { path: "", source: "path", available: false, error: "找不到 ffmpeg" },
  },
  yt_dlp: { pinned: null, previous: "2026.10.01" },
};

const TOKENS = {
  tokens: [
    {
      id: "t1",
      name: "claude-code",
      prefix: "nst_ab",
      scopes: ["agent", "read"],
      created_at: "2026-11-01T00:00:00Z",
    },
    {
      id: "t2",
      name: "old",
      prefix: "nst_cd",
      scopes: ["read"],
      created_at: "2026-10-01T00:00:00Z",
      revoked_at: "2026-10-02T00:00:00Z",
    },
  ],
};

const LANGUAGE = {
  targets: ["zh-Hant-TW"],
  caption_priority: [
    { lang: "original", kind: "manual" },
    { lang: "original", kind: "auto" },
  ],
  allow_platform_translated: false,
  fallback: ["supadata", "groq"],
  variant_strategy: "convert",
};

function settingsApi(routes: Record<string, MockHandler> = {}) {
  return mockApi({
    "GET /api/settings/services": () => ({ providers: { groq: { api_key_last4: "9z9z" } } }),
    "GET /api/settings/supadata": () => ({ mode: "auto" }),
    "GET /api/settings/language": () => LANGUAGE,
    "GET /api/settings/agent": () => ({}),
    "GET /api/tokens": () => TOKENS,
    "GET /api/system": () => SYSTEM,
    "GET /api/glossaries/{scope}/{id}": (req) => ({
      scope: req.params.scope,
      id: req.params.id,
      terms: ["Notesteme"],
      updated_at: null,
    }),
    "GET /api/channels/{id}/term-candidates": () => ({
      candidates: [
        {
          id: "c1",
          term: "码农高天",
          state: "pending",
          item_id: "",
          created_at: "2026-11-20T00:00:00Z",
        },
        {
          id: "c2",
          term: "CPython",
          state: "accepted",
          item_id: "",
          created_at: "2026-11-19T00:00:00Z",
        },
      ],
    }),
    // UI-4：通知與預算（行為測試在 notifications.test.tsx）
    "GET /api/notifiers": () => ({ notifiers: [notifier("ops")] }),
    "GET /api/notifications": () => ({ notifications: [] }),
    "GET /api/settings/budget": () => ({ monthly_usd: 20, warn_at_percent: 80, timezone: "UTC" }),
    ...routes,
  });
}

async function openSection(section: string) {
  signIn();
  const utils = renderRoute(`/settings/${section}`);
  await screen.findByRole("heading", { level: 1, name: "設定" });
  return utils;
}

describe("設定頁的版面", () => {
  it("左側分區清單標出目前的分區，窄螢幕的下拉選單也能切換", async () => {
    settingsApi();
    const { router } = await openSection("language");
    const nav = screen.getByRole("navigation", { name: "設定分區" });
    expect(within(nav).getByRole("link", { name: "語言" }).getAttribute("aria-current")).toBe(
      "page",
    );
    expect(within(nav).getAllByRole("link")).toHaveLength(SETTINGS_SECTIONS.length);
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "設定分區" }), "system");
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/system"));
    expect(await screen.findByText("外部工具")).toBeTruthy();
  });
});

describe("每個分區沒有 serious／critical 的 axe 問題", () => {
  for (const s of SETTINGS_SECTIONS) {
    it(s, async () => {
      settingsApi();
      const { container } = await openSection(s);
      // 等資料載入（有載入中的分區）
      await waitFor(() => expect(container.querySelector(".spinner")).toBeNull());
      await expectNoA11yViolations(container);
    });
  }
});

describe("服務與金鑰", () => {
  it("只顯示末四碼；更換時只送那一個 provider，送出後輸入框清空、key 不留在畫面上", async () => {
    let body: unknown;
    const api = settingsApi({
      "PUT /api/settings/{section}": (req) => {
        body = req.body;
        return {
          providers: { groq: { api_key_last4: "9z9z" }, openai: { api_key_last4: "1234" } },
        };
      },
    });
    await openSection("services");
    expect(await screen.findByText("已設定（末四碼 9z9z）")).toBeTruthy();
    expect(screen.getAllByText("尚未設定").length).toBeGreaterThan(0);

    const rows = screen.getAllByRole("listitem");
    const openai = rows.find((r) => within(r).queryByText("OpenAI"))!;
    await userEvent.click(within(openai).getByRole("button", { name: "設定 API key" }));
    const input = within(openai).getByLabelText("OpenAI 的 API key") as HTMLInputElement;
    expect(input.type).toBe("password");
    expect(input.autocomplete).toBe("new-password");
    await userEvent.type(input, SECRET_KEY);
    await userEvent.click(within(openai).getByRole("button", { name: "儲存 API key" }));

    await screen.findByText("已設定（末四碼 1234）");
    expect(body).toEqual({ providers: { openai: { api_key: SECRET_KEY } } });
    expect(api.calls("PUT", "/api/settings/services")).toHaveLength(1);
    expect(document.body.innerHTML).not.toContain(SECRET_KEY);
    expect(localStorage.length).toBe(0);
  });

  it("清除 key 要先確認", async () => {
    let body: unknown;
    settingsApi({
      "PUT /api/settings/{section}": (req) => {
        body = req.body;
        return { providers: {} };
      },
    });
    await openSection("services");
    await userEvent.click(await screen.findByRole("button", { name: "清除 API key" }));
    const dialog = await screen.findByRole("dialog", { name: "清除 Groq 的 API key？" });
    expect(body).toBeUndefined();
    await userEvent.click(within(dialog).getByRole("button", { name: "清除 API key" }));
    await waitFor(() => expect(body).toEqual({ providers: { groq: { api_key: "" } } }));
  });

  it("後端沒有測試連線的 API：明確說明，不放假的按鈕", async () => {
    settingsApi();
    await openSection("services");
    expect(await screen.findByText(/「測試連線」與模型的停用公告/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: /測試連線/ })).toBeNull();
  });
});

describe("帳號與 Cookie", () => {
  it("上傳 cookie：送出後清空，顯示這次上傳的數量", async () => {
    let body: unknown;
    settingsApi({
      "POST /api/cookies": (req) => {
        body = req.body;
        return { platform: "youtube", cookies: 3, updated_at: "2026-11-23T01:02:03Z" };
      },
    });
    await openSection("accounts");
    const box = screen.getAllByLabelText("貼上 cookie")[0] as HTMLTextAreaElement;
    expect(box.getAttribute("autocomplete")).toBe("off");
    fireEvent.change(box, { target: { value: "SID=secret-cookie-value" } });
    await userEvent.click(screen.getByRole("button", { name: "上傳 YouTube cookie" }));
    expect(await screen.findByText(/已儲存 3 個 cookie/)).toBeTruthy();
    expect(body).toEqual({ platform: "youtube", content: "SID=secret-cookie-value" });
    expect(box.value).toBe("");
    expect(document.body.innerHTML).not.toContain("secret-cookie-value");
  });

  it("錯誤依 code 顯示原因與下一步", async () => {
    settingsApi({
      "POST /api/cookies": () => apiError(422, "cookie_missing_sessdata", "raw message"),
    });
    await openSection("accounts");
    const box = screen.getAllByLabelText("貼上 cookie")[1]!;
    fireEvent.change(box, { target: { value: "a=b" } });
    await userEvent.click(screen.getByRole("button", { name: "上傳 Bilibili cookie" }));
    expect(
      await screen.findByText("Bilibili cookie 缺少 SESSDATA。登入 Bilibili 後重新匯出 cookie。"),
    ).toBeTruthy();
  });
});

describe("MCP 與 Agent", () => {
  it("建立的 token 只在對話框顯示一次，關閉後畫面上就沒有了", async () => {
    let body: unknown;
    const writeText = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    settingsApi({
      "POST /api/tokens": (req) => {
        body = req.body;
        return {
          token: TOKEN,
          id: "t3",
          name: "bot",
          prefix: "nst_PL",
          scopes: ["agent"],
          created_at: "2026-11-23T00:00:00Z",
        };
      },
    });
    await openSection("agent");
    expect(await screen.findByText("claude-code")).toBeTruthy();
    expect(screen.getByText("已撤銷")).toBeTruthy();
    await userEvent.type(screen.getByLabelText("名稱"), "bot");
    await userEvent.click(screen.getByRole("button", { name: "建立 token" }));

    const dialog = await screen.findByRole("dialog", { name: "已建立 bot" });
    expect((within(dialog).getByLabelText("token") as HTMLInputElement).value).toBe(TOKEN);
    expect(body).toEqual({ name: "bot", scopes: ["agent"], expires_in: "90d" });
    await userEvent.click(within(dialog).getByRole("button", { name: "複製 token" }));
    expect(writeText).toHaveBeenCalledWith(TOKEN);
    expect(await within(dialog).findByText("已複製")).toBeTruthy();

    await userEvent.click(within(dialog).getByRole("button", { name: "關閉" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.body.innerHTML).not.toContain(TOKEN);
    expect(localStorage.length).toBe(0);
  });

  it("撤銷要先確認", async () => {
    const api = settingsApi({ "DELETE /api/tokens/{id}": () => ({ revoked: true }) });
    await openSection("agent");
    await userEvent.click(await screen.findByRole("button", { name: "撤銷 claude-code" }));
    const dialog = await screen.findByRole("dialog", { name: "撤銷 claude-code？" });
    expect(
      within(dialog).getByText("撤銷後，使用這個 token 的程式會立即失去存取權。"),
    ).toBeTruthy();
    expect(api.calls("DELETE", "/api/tokens/{id}")).toHaveLength(0);
    await userEvent.click(within(dialog).getByRole("button", { name: "撤銷" }));
    await waitFor(() => expect(api.calls("DELETE", "/api/tokens/t1")).toHaveLength(1));
  });

  it("Agent 設定只送有填的欄位；422 逐欄位顯示", async () => {
    let body: unknown;
    settingsApi({
      "PUT /api/settings/{section}": (req) => {
        body = req.body;
        return apiError(422, "invalid_request", "設定不正確", {
          errors: [{ path: "agent.lease", message: "不是正確的時間長度。" }],
        });
      },
    });
    await openSection("agent");
    await userEvent.type(await screen.findByLabelText("租約時長"), "soon");
    await userEvent.click(screen.getByRole("button", { name: "儲存變更" }));
    expect(await screen.findByText("不是正確的時間長度。")).toBeTruthy();
    expect(screen.getByText("agent.lease")).toBeTruthy();
    expect(body).toEqual({ lease: "soon" });
  });
});

describe("登入：修改密碼", () => {
  it("目前的密碼錯誤時說明原因，欄位清空", async () => {
    settingsApi({
      "POST /api/auth/password": () => apiError(422, "invalid_password", "密碼不正確。"),
    });
    await openSection("login");
    const current = screen.getByLabelText("目前的密碼") as HTMLInputElement;
    expect(current.autocomplete).toBe("current-password");
    await userEvent.type(current, "wrong");
    await userEvent.type(screen.getByLabelText("新密碼"), "n1");
    await userEvent.type(screen.getByLabelText("再輸入一次新密碼"), "n1");
    await userEvent.click(screen.getByRole("button", { name: "修改密碼" }));
    expect(await screen.findByText(/目前的密碼不正確/)).toBeTruthy();
    expect(current.value).toBe("");
  });

  it("兩次新密碼不同時不送出", async () => {
    const api = settingsApi();
    await openSection("login");
    await userEvent.type(screen.getByLabelText("目前的密碼"), "pw");
    await userEvent.type(screen.getByLabelText("新密碼"), "a");
    await userEvent.type(screen.getByLabelText("再輸入一次新密碼"), "b");
    await userEvent.click(screen.getByRole("button", { name: "修改密碼" }));
    expect(await screen.findByText("兩次輸入的新密碼不同。")).toBeTruthy();
    expect(api.calls("POST", "/api/auth/password")).toHaveLength(0);
  });

  it("成功時顯示 toast", async () => {
    let body: unknown;
    settingsApi({
      "POST /api/auth/password": (req) => {
        body = req.body;
        return { changed: true };
      },
    });
    await openSection("login");
    await userEvent.type(screen.getByLabelText("目前的密碼"), "pw");
    await userEvent.type(screen.getByLabelText("新密碼"), "pw2");
    await userEvent.type(screen.getByLabelText("再輸入一次新密碼"), "pw2");
    await userEvent.click(screen.getByRole("button", { name: "修改密碼" }));
    expect(await screen.findByText("已修改密碼，其他裝置已登出")).toBeTruthy();
    expect(body).toEqual({ current_password: "pw", new_password: "pw2" });
  });
});

describe("詞彙表與術語候選", () => {
  async function openChannel() {
    await openSection("glossary");
    await userEvent.type(await screen.findByLabelText("頻道"), "youtube:UC123");
    await userEvent.click(screen.getByRole("button", { name: "開啟頻道" }));
    return screen.findByRole("button", { name: "接受 码农高天" });
  }

  it("接受候選送 PATCH，並重新讀取頻道詞彙表", async () => {
    let body: unknown;
    const api = settingsApi({
      "PATCH /api/channels/{id}/term-candidates/{candidate}": (req) => {
        body = req.body;
        return { id: "c1", term: "码农高天", state: "accepted", item_id: "", created_at: "" };
      },
    });
    await userEvent.click(await openChannel());
    expect(await screen.findByText("已接受 码农高天，已加進頻道詞彙表")).toBeTruthy();
    expect(body).toEqual({ state: "accepted" });
    expect(api.calls("PATCH", "/api/channels/youtube%3AUC123/term-candidates/c1")).toHaveLength(1);
    await waitFor(() =>
      expect(api.calls("GET", "/api/glossaries/channel/youtube%3AUC123").length).toBeGreaterThan(1),
    );
  });

  it("409 term_candidate_decided：顯示已被處理，並重新整理列表", async () => {
    const api = settingsApi({
      "PATCH /api/channels/{id}/term-candidates/{candidate}": () =>
        apiError(409, "term_candidate_decided", "已經決定"),
    });
    await userEvent.click(await openChannel());
    expect(await screen.findByText(/「码农高天」已被處理/)).toBeTruthy();
    await waitFor(() =>
      expect(api.calls("GET", "/api/channels/{id}/term-candidates").length).toBeGreaterThan(1),
    );
  });

  it("儲存全域詞彙表：一行一個詞", async () => {
    let body: unknown;
    settingsApi({
      "PUT /api/glossaries/{scope}/{id}": (req) => {
        body = req.body;
        return { scope: "global", id: "default", terms: ["A", "B"], updated_at: null };
      },
    });
    await openSection("glossary");
    const box = (await screen.findAllByLabelText("詞彙（一行一個）"))[0]!;
    fireEvent.change(box, { target: { value: "A\n\n  B  \n" } });
    await userEvent.click(screen.getByRole("button", { name: "儲存詞彙表" }));
    expect(await screen.findByText("已儲存詞彙表（2 個詞）")).toBeTruthy();
    expect(body).toEqual({ terms: ["A", "B"] });
  });
});

describe("匯出與匯入", () => {
  function chooseFile(content: string, name = "conf.yaml") {
    const input = screen.getByLabelText("選擇 YAML 檔", { selector: "input" }) as HTMLInputElement;
    const file = new File([content], name, { type: "application/yaml" });
    return act(async () => {
      fireEvent.change(input, { target: { files: [file] } });
    });
  }

  it("匯入前先確認；422 時逐項顯示錯誤", async () => {
    let body: unknown;
    settingsApi({
      "POST /api/config/import": (req) => {
        body = req.body;
        return apiError(422, "invalid_request", "設定檔有錯誤", {
          errors: [
            {
              path: "profiles.notes.stages.correct.executor",
              message: "必須是 api、agent 或 off。",
            },
            { path: "default_profile", message: "沒有這個處理方案。" },
          ],
        });
      },
    });
    await openSection("config");
    await chooseFile("profiles: {}\n");
    await screen.findByText("已選擇 conf.yaml");
    await userEvent.click(screen.getByRole("button", { name: "匯入" }));
    const dialog = await screen.findByRole("dialog", { name: "匯入 conf.yaml？" });
    expect(body).toBeUndefined();
    await userEvent.click(within(dialog).getByRole("button", { name: "匯入" }));
    expect(await screen.findByText("必須是 api、agent 或 off。")).toBeTruthy();
    expect(screen.getByText("沒有這個處理方案。")).toBeTruthy();
    expect(screen.getByText("profiles.notes.stages.correct.executor")).toBeTruthy();
    expect(body).toEqual({ content: "profiles: {}\n" });
  });

  it("匯入成功列出變更", async () => {
    settingsApi({
      "POST /api/config/import": () => ({
        created: ["notes"],
        updated: [],
        unchanged: ["fast"],
        default_changed: true,
        default_profile: "notes",
      }),
    });
    await openSection("config");
    await chooseFile("x: 1\n");
    await userEvent.click(await screen.findByRole("button", { name: "匯入" }));
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "匯入" }),
    );
    expect(await screen.findByText("已改成 notes")).toBeTruthy();
    expect(screen.getByText("fast")).toBeTruthy();
  });

  it("匯出下載 YAML", async () => {
    const createObjectURL = vi.fn(() => "blob:x");
    const revokeObjectURL = vi.fn();
    Object.assign(URL, { createObjectURL, revokeObjectURL });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    settingsApi({
      "GET /api/config/export": () => ({ content: "profiles: {}\n", profiles: ["notes"] }),
    });
    await openSection("config");
    await userEvent.click(screen.getByRole("button", { name: "匯出 YAML" }));
    expect(await screen.findByText("已匯出：notes")).toBeTruthy();
    expect(click).toHaveBeenCalled();
    await waitFor(() => expect(revokeObjectURL).toHaveBeenCalledWith("blob:x"), { timeout: 3000 });
    click.mockRestore();
  });
});

describe("系統", () => {
  it("顯示工具狀態；更新 yt-dlp 後顯示新版本", async () => {
    settingsApi({
      "POST /api/tools/yt-dlp/update": () => ({
        version: "2026.11.08",
        rolled_back: false,
        skipped: false,
        pinned: "",
        previous: "2026.10.01",
        message: "",
      }),
    });
    await openSection("system");
    expect(await screen.findByText("無法使用：找不到 ffmpeg")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "更新 yt-dlp" }));
    expect((await screen.findAllByText("已更新到 2026.11.08")).length).toBeGreaterThan(0);
  });
});

describe("語言與外觀", () => {
  it("語言分區送出完整的區段", async () => {
    let body: unknown;
    settingsApi({
      "PUT /api/settings/{section}": (req) => {
        body = req.body;
        return req.body;
      },
    });
    await openSection("language");
    const targets = await screen.findByLabelText("預設目標語言");
    await userEvent.clear(targets);
    await userEvent.type(targets, "zh-Hant-TW, en");
    await userEvent.click(screen.getByRole("button", { name: "第 2 條上移" }));
    await userEvent.click(screen.getByRole("button", { name: "儲存變更" }));
    await waitFor(() =>
      expect(body).toEqual({
        ...LANGUAGE,
        targets: ["zh-Hant-TW", "en"],
        caption_priority: [
          { lang: "original", kind: "auto" },
          { lang: "original", kind: "manual" },
        ],
      }),
    );
  });

  it("外觀分區與導覽底部的主題切換是同一個值", async () => {
    settingsApi();
    await openSection("appearance");
    await userEvent.click(screen.getByRole("radio", { name: "深色" }));
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect((screen.getByLabelText("主題", { selector: "select" }) as HTMLSelectElement).value).toBe(
      "dark",
    );
    await userEvent.click(screen.getByRole("radio", { name: "跟隨系統" }));
    expect(document.documentElement.dataset.theme).toBeUndefined();
  });
});

describe("書籤小工具（ENTRY-R1）", () => {
  it("連結是依目前 origin 產生的 bookmarklet，在這個頁面點擊不執行", async () => {
    settingsApi();
    await openSection("bookmarklet");
    const link = await screen.findByRole("link", { name: "加到 Notesteme" });
    await waitFor(() =>
      expect(link.getAttribute("href")).toBe(
        `javascript:(()=>{window.open('${window.location.origin}/add?url='+encodeURIComponent(location.href),'_blank','noopener')})()`,
      ),
    );
    const ev = new MouseEvent("click", { bubbles: true, cancelable: true });
    link.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
    expect(screen.getByText(`${window.location.origin}/add?url={url}`)).toBeTruthy();
  });
});
