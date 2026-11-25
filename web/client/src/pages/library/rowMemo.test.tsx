// NFR-PERF-10 的回歸測試（docs/ui/README.md 10.32）：媒體庫的列是 memo 的。
// 虛擬列表每次捲動都重新渲染，以前可視範圍內的每一列都跟著重算（10,000 列捲動出現 400ms 的長工作）。
// 這裡用「勾選一列」觸發整個列表重新渲染，確認只有那一列重算（以 formatDate 的呼叫次數計）。
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import * as i18n from "../../i18n";
import { detail, item, stage } from "../../test/fixtures";
import { mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";

vi.mock("../../i18n", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../../i18n")>();
  return { ...mod, formatDate: vi.fn(mod.formatDate) };
});

const all = [item(1), item(2), item(3), item(4)];

describe("媒體庫的列（memo）", () => {
  it("勾選一列時其他列不重新渲染；Shift 範圍選取照常運作", async () => {
    mockApi({
      "GET /api/items": () => ({ items: all, total: all.length }),
      "GET /api/items/{id}": (req) =>
        detail(all.find((x) => x.id === req.params.id) ?? item(9), [stage("metadata", "done")]),
    });
    signIn();
    renderRoute("/library?group=none");
    const user = userEvent.setup();
    const first = await screen.findByRole("checkbox", { name: "選取「影片 1」" });
    // 每一列的狀態都讀完（detail query 讓列重新渲染）之後才開始計算
    await waitFor(() => expect(screen.queryAllByText("正在載入狀態…")).toHaveLength(0));
    const formatDate = vi.mocked(i18n.formatDate);
    formatDate.mockClear();
    await user.click(first);
    expect((first as HTMLInputElement).checked).toBe(true);
    expect(formatDate.mock.calls.length, "勾選一列卻讓其他列重新渲染").toBeLessThanOrEqual(1);

    // 經由 ref 呼叫的 toggle 仍是最新的選取：Shift 點第 3 列，選取 1～3
    await user.keyboard("{Shift>}");
    await user.click(screen.getByRole("checkbox", { name: "選取「影片 3」" }));
    await user.keyboard("{/Shift}");
    const checked = (n: number) =>
      (screen.getByRole("checkbox", { name: `選取「影片 ${n}」` }) as HTMLInputElement).checked;
    expect([1, 2, 3, 4].map(checked)).toEqual([true, true, true, false]);
  });
});
