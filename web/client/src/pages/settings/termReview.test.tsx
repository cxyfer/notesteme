import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { apiError, mockApi, type MockHandler } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { termReview } from "../../test/ui5Fixtures";

function reviewApi(extra: Record<string, MockHandler> = {}) {
  return mockApi({
    "GET /api/glossaries/{scope}/{id}": (req) => ({
      scope: req.params.scope,
      id: req.params.id,
      terms: [],
      updated_at: null,
    }),
    "GET /api/term-candidates": () => ({
      candidates: [
        termReview("a", "Kubernetees", "c1"),
        termReview("b", "Rust", "c2", { item: null as never }),
        termReview("c", "Go", "c1"),
      ],
      total: 3,
    }),
    ...extra,
  });
}

async function open() {
  signIn();
  const r = renderRoute("/settings/glossary");
  await screen.findByRole("heading", { level: 1, name: "設定" });
  await screen.findByText("Kubernetees");
  return r;
}

describe("設定：術語審核（GLOS-R7）", () => {
  it("依頻道分組，列出來源影音；影音已刪除時說明（axe 無違規）", async () => {
    const api = reviewApi();
    const { container } = await open();
    const groups = screen.getAllByRole("heading", { level: 4 }).map((h) => h.textContent);
    expect(groups).toEqual(["頻道 c1 · youtube:UCc1", "頻道 c2 · youtube:UCc2"]);
    expect(screen.getByText(/來源影音已刪除/)).toBeTruthy();
    expect(api.calls("GET", "/api/term-candidates")[0]!.url.search).toBe("?state=pending&limit=50");
    await expectNoA11yViolations(container);
  });

  it("改寫後接受：輸入框預填原詞，可以選加到全域", async () => {
    let body: unknown;
    reviewApi({
      "POST /api/term-candidates/decide": (req) => {
        body = req.body;
        return { candidates: [{ id: "a", term: "Kubernetees", state: "accepted" }] };
      },
    });
    await open();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "改寫後接受 Kubernetees" }));
    const input = screen.getByLabelText("把「Kubernetees」改寫成") as HTMLInputElement;
    expect(input.value).toBe("Kubernetees");
    await user.clear(input);
    await user.type(input, "Kubernetes");
    await user.selectOptions(screen.getByLabelText("加到哪裡"), "global");
    await user.click(screen.getByRole("button", { name: "接受改寫" }));
    expect(await screen.findByText("已接受 1 項、拒絕 0 項")).toBeTruthy();
    expect(body).toEqual({
      decisions: [{ candidate: "a", state: "accepted", scope: "global", term: "Kubernetes" }],
    });
  });

  it("多選後一次拒絕；422 的 decisions.<n> 對回那一列", async () => {
    const bodies: unknown[] = [];
    reviewApi({
      "POST /api/term-candidates/decide": (req) => {
        bodies.push(req.body);
        return bodies.length === 1
          ? apiError(422, "invalid_request", "請求不正確", {
              errors: [{ path: "decisions.1.term", message: "術語不能是空白。" }],
            })
          : { candidates: [] };
      },
    });
    await open();
    const user = userEvent.setup();
    await user.click(screen.getByLabelText("選取 Rust"));
    await user.click(screen.getByLabelText("選取 Go"));
    expect(screen.getByText("已選 2 項")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "拒絕所選" }));
    expect(bodies[0]).toEqual({
      decisions: [
        { candidate: "b", state: "rejected" },
        { candidate: "c", state: "rejected" },
      ],
    });
    const row = (await screen.findByText("術語不能是空白。")).closest("li")!;
    expect(within(row).getByText("Go")).toBeTruthy();
  });

  it("409 glossary_full 顯示伺服器的訊息；term_candidate_decided 重新讀取", async () => {
    let n = 0;
    const api = reviewApi({
      "POST /api/term-candidates/decide": () =>
        ++n === 1
          ? apiError(409, "glossary_full", "頻道的詞彙表已有 500 個詞，無法再加入。")
          : apiError(409, "term_candidate_decided", "已經決定"),
    });
    await open();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "接受 Go" }));
    expect(await screen.findByText("頻道的詞彙表已有 500 個詞，無法再加入。")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "拒絕 Go" }));
    expect(await screen.findByText(/有術語已被處理/)).toBeTruthy();
    await waitFor(() => expect(api.calls("GET", "/api/term-candidates").length).toBeGreaterThan(2));
  });

  it("篩選狀態與頻道", async () => {
    const api = reviewApi();
    await open();
    const user = userEvent.setup();
    await user.selectOptions(screen.getByLabelText("狀態"), "all");
    await user.type(screen.getByLabelText("頻道（選填）"), "youtube:UCc1");
    await user.click(screen.getByRole("button", { name: "套用" }));
    await waitFor(() =>
      expect(api.calls("GET", "/api/term-candidates").at(-1)!.url.search).toBe(
        "?state=all&channel=youtube%3AUCc1&limit=50",
      ),
    );
  });
});
