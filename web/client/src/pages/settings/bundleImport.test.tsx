import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes } from "../../test/fixtures";
import { apiError, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";

const SYSTEM = {
  listen: "127.0.0.1:8080",
  listen_source: "default",
  data: "/data",
  data_source: "env",
  tools: {},
  yt_dlp: { pinned: null, previous: null },
  alerts: [],
  backup: { last_ok_at: null, last_error: null, next_run_at: null },
};

function bundleApi(handler: MockHandler) {
  return mockApi({ "GET /api/system": () => SYSTEM, "POST /api/bundles/import": handler });
}

async function open() {
  signIn();
  const r = renderRoute("/settings/system");
  await screen.findByRole("heading", { level: 3, name: "匯入 bundle" });
  return r;
}

const zip = () => new File(["PK\x03\x04"], "a.notesteme.zip", { type: "application/zip" });

describe("設定：匯入 bundle（BND）", () => {
  it("上傳 multipart；勾選加成新版本送 on_existing；成功連到影音（axe 無違規）", async () => {
    let form: FormData | undefined;
    const api = bundleApi((req) => {
      form = req.init.body as FormData;
      return { item: { id: "item-9", ref: "yt:x", title: "匯入的影片" }, imported: 3, skipped: 1 };
    });
    const { container } = await open();
    const user = userEvent.setup();
    const button = screen.getByRole("button", { name: "匯入" });
    expect(button.hasAttribute("disabled")).toBe(true);
    await user.upload(screen.getByLabelText("bundle 檔案"), zip());
    expect(screen.getByText(/a\.notesteme\.zip（/)).toBeTruthy();
    await user.click(screen.getByLabelText("影音已存在時，把產出加成新版本"));
    await user.click(button);
    expect(await screen.findByText(/已匯入 3 個產出，略過 1 個相同的版本/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "匯入的影片" }).getAttribute("href")).toBe(
      "/items/item-9",
    );
    expect(api.calls("POST", "/api/bundles/import")[0]!.url.search).toBe(
      "?on_existing=add_versions",
    );
    expect((form!.get("file") as File).name).toBe("a.notesteme.zip");
    await expectNoA11yViolations(container);
  });

  it("422 顯示訊息與出錯的項目；413 顯示伺服器的上限訊息", async () => {
    let n = 0;
    bundleApi(() =>
      ++n === 1
        ? apiError(422, "bundle_invalid", "bundle 中的項目「../x」含有「..」，已拒絕匯入。", {
            entry: "../x",
          })
        : apiError(413, "bundle_too_large", "bundle 超過 64 MiB 的上傳上限。"),
    );
    await open();
    const user = userEvent.setup();
    await user.upload(screen.getByLabelText("bundle 檔案"), zip());
    await user.click(screen.getByRole("button", { name: "匯入" }));
    expect(await screen.findByText(/含有「\.\.」，已拒絕匯入/)).toBeTruthy();
    expect(screen.getByText("../x").tagName).toBe("CODE");
    await user.click(screen.getByRole("button", { name: "匯入" }));
    expect(await screen.findByText("bundle 超過 64 MiB 的上傳上限。")).toBeTruthy();
  });

  it("409 已存在：提示勾選加成新版本，並連到既有的影音（item_id）", async () => {
    bundleApi(() =>
      apiError(409, "bundle_item_exists", "這部影音已在媒體庫中（yt:x）。請加上 on_existing。", {
        item_id: "item-7",
      }),
    );
    await open();
    const user = userEvent.setup();
    await user.upload(screen.getByLabelText("bundle 檔案"), zip());
    await user.click(screen.getByRole("button", { name: "匯入" }));
    expect(await screen.findByText(/勾選「影音已存在時/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "打開既有的影音" }).getAttribute("href")).toBe(
      "/items/item-7",
    );
    await user.click(screen.getByRole("button", { name: "影音已存在時，把產出加成新版本" }));
    await waitFor(() =>
      expect(
        (screen.getByLabelText("影音已存在時，把產出加成新版本") as HTMLInputElement).checked,
      ).toBe(true),
    );
  });

  it("409 在垃圾桶：沒有 item_id，提示並連到垃圾桶", async () => {
    bundleApi(() => apiError(409, "bundle_item_in_trash", "這部影音在垃圾桶中。"));
    await open();
    const user = userEvent.setup();
    await user.upload(screen.getByLabelText("bundle 檔案"), zip());
    await user.click(screen.getByRole("button", { name: "匯入" }));
    expect(await screen.findByText(/這部影音在垃圾桶中。先還原/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "前往垃圾桶" }).getAttribute("href")).toBe("/trash");
    expect(screen.queryByRole("link", { name: "打開既有的影音" })).toBeNull();
  });

  it("影音詳情有匯出 bundle 的下載連結", async () => {
    mockApi(defaultRoutes());
    signIn();
    renderRoute("/items/item-1");
    const link = await screen.findByRole("link", { name: "匯出 bundle" });
    expect(link.getAttribute("href")).toBe("/api/items/item-1/bundle");
    expect(link.hasAttribute("download")).toBe(true);
  });
});
