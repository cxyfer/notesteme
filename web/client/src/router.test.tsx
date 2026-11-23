import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { renderRoute, signIn } from "./test/render";

describe("路由", () => {
  it("沒有 session 時導到登入頁並記住原本的路徑", async () => {
    const { router } = renderRoute("/queue?x=1");
    await screen.findByRole("heading", { level: 1, name: "notesteme" });
    expect(router.state.location.pathname).toBe("/login");
    expect(router.state.location.search).toEqual({ redirect: "/queue?x=1" });
  });

  it("GET / 是總覽（UI-7）", async () => {
    signIn();
    const { router } = renderRoute("/");
    await screen.findByRole("heading", { level: 1, name: "總覽" });
    expect(router.state.location.pathname).toBe("/");
  });

  it("/settings 導到第一個分區", async () => {
    signIn();
    const { router } = renderRoute("/settings");
    await screen.findByRole("heading", { level: 2, name: "服務與金鑰" });
    expect(router.state.location.pathname).toBe("/settings/services");
  });

  it("導覽列出已完成的頁面（M1 加上 UI-3a 的調整三頁），目前頁面標成 aria-current", async () => {
    signIn();
    renderRoute("/queue");
    const nav = await screen.findByRole("navigation", { name: "主要導覽" });
    const links = [...nav.querySelectorAll("a.nav__item")].map((a) => a.textContent);
    expect(links).toEqual([
      "總覽",
      "媒體庫",
      "佇列",
      "訂閱",
      "Prompt 庫",
      "處理方案",
      "字幕樣式",
      "筆記模板",
      "成本",
      "設定",
    ]);
    expect(screen.getByRole("link", { name: "佇列" }).getAttribute("aria-current")).toBe("page");
  });

  it("skip link 是第一個可聚焦的元素，指向 #main", async () => {
    signIn();
    renderRoute("/library");
    await screen.findByRole("heading", { level: 1, name: "媒體庫" });
    await userEvent.tab();
    const skip = screen.getByRole("link", { name: "跳到主要內容" });
    expect(document.activeElement).toBe(skip);
    expect(skip.getAttribute("href")).toBe("#main");
  });

  it("未知的設定分區導到第一個分區", async () => {
    signIn();
    const { router } = renderRoute("/settings/nope");
    await screen.findByRole("heading", { level: 2, name: "服務與金鑰" });
    expect(router.state.location.pathname).toBe("/settings/services");
  });

  it("切換介面語言時更新 <html lang>", async () => {
    signIn();
    renderRoute("/library");
    await screen.findByRole("heading", { level: 1, name: "媒體庫" });
    await userEvent.selectOptions(screen.getByLabelText("介面語言"), "en");
    await screen.findByRole("heading", { level: 1, name: "Library" });
    expect(document.documentElement.lang).toBe("en");
    await userEvent.selectOptions(screen.getByLabelText("Interface language"), "zh-Hans");
    await waitFor(() => expect(document.documentElement.lang).toBe("zh-Hans"));
  });

  it("切換主題寫入 data-theme 與 localStorage", async () => {
    signIn();
    renderRoute("/library");
    await userEvent.selectOptions(await screen.findByLabelText("主題"), "dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(localStorage.getItem("notesteme.theme")).toBe("dark");
    await userEvent.selectOptions(screen.getByLabelText("主題"), "system");
    expect(document.documentElement.dataset.theme).toBeUndefined();
  });
});
