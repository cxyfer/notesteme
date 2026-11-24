import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";

describe("設定：輸出", () => {
  it("唯讀列出設定檔的 vault（adapter 與根資料夾）與 hook，沒有編輯的入口（VAULT 的 @ui 場景）", async () => {
    mockApi({
      "GET /api/vaults": () => ({
        vaults: [
          { id: "main", adapter: "fs", base: "Video Notes" },
          { id: "remote", adapter: "git", base: "Notes" },
        ],
      }),
      "GET /api/hooks": () => ({ hooks: [{ id: "notify" }] }),
    });
    signIn();
    const { container } = renderRoute("/settings/outputs");
    const vaults = await screen.findByRole("list", { name: "Vault" });
    const main = within(vaults).getByText("main").closest("li")!;
    expect(within(main).getByText("本機資料夾（fs）")).toBeTruthy();
    expect(within(main).getByText("根資料夾：Video Notes")).toBeTruthy();
    expect(within(vaults).getByText("git repo（git）")).toBeTruthy();
    const hooks = await screen.findByRole("list", { name: "寫入後執行的 hook" });
    expect(within(hooks).getByText("notify")).toBeTruthy();
    // 沒有輸入框或按鈕可以修改路徑
    expect(screen.queryAllByRole("textbox")).toHaveLength(0);
    expect(screen.queryByText("尚未啟用（M3 起提供）。")).toBeNull();
    await expectNoA11yViolations(container);
  });

  it("設定檔沒有定義時寫明", async () => {
    mockApi({
      "GET /api/vaults": () => ({ vaults: [] }),
      "GET /api/hooks": () => ({ hooks: [] }),
    });
    signIn();
    renderRoute("/settings/outputs");
    expect(await screen.findByText("設定檔沒有定義任何 vault。")).toBeTruthy();
    expect(await screen.findByText("設定檔沒有定義任何 hook。")).toBeTruthy();
  });
});
