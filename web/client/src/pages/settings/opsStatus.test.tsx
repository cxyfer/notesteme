import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { formatElapsed } from "./OpsStatus";

function system(over: Record<string, unknown> = {}) {
  return {
    listen: "127.0.0.1:8080",
    listen_source: "default",
    data: "/data",
    data_source: "env",
    tools: {},
    yt_dlp: { pinned: null, previous: null },
    alerts: [],
    backup: { last_ok_at: null, last_error: null, next_run_at: null },
    ...over,
  };
}

async function open(sys: ReturnType<typeof system>) {
  mockApi({ "GET /api/system": () => sys });
  signIn();
  const r = renderRoute("/settings/system");
  await screen.findByRole("heading", { level: 1, name: "設定" });
  await screen.findByRole("heading", { level: 3, name: "系統告警" });
  return r;
}

describe("設定：系統告警與備份（OPS）", () => {
  it("經過的時間依長度選單位", () => {
    expect(formatElapsed(5 * 60_000, "en")).toBe("5 minutes");
    expect(formatElapsed(3 * 3600_000, "en")).toBe("3 hours");
    expect(formatElapsed(3 * 86400_000, "en")).toBe("3 days");
  });

  it("列出告警、持續時間與備份失敗的原因（axe 無違規）", async () => {
    const since = new Date(Date.now() - 2 * 3600_000 - 60_000).toISOString();
    const { container } = await open(
      system({
        alerts: [
          { kind: "disk_usage", key: "/data", message: "/data 已使用 93%。", since },
          { kind: "new_kind", key: "x", message: "新的告警", since },
        ],
        backup: {
          last_ok_at: "2026-11-22T03:00:00Z",
          last_error: "空間不足：需要 2 GiB。",
          next_run_at: "2026-11-25T03:00:00Z",
        },
      }),
    );
    expect(screen.getByText("磁碟空間不足")).toBeTruthy();
    expect(screen.getByText("/data 已使用 93%。")).toBeTruthy();
    expect(screen.getAllByText(/已持續 2 小時/)).toHaveLength(2);
    // 認不得的種類原樣顯示
    expect(screen.getByText("new_kind")).toBeTruthy();
    expect(screen.getByText("最近一次備份失敗")).toBeTruthy();
    expect(screen.getByText("空間不足：需要 2 GiB。")).toBeTruthy();
    await expectNoA11yViolations(container);
  });

  it("沒有告警；備份停用且還沒成功過", async () => {
    await open(system());
    expect(screen.getByText("目前沒有告警。")).toBeTruthy();
    expect(screen.getByText("還沒有成功過")).toBeTruthy();
    expect(screen.getByText("已停用")).toBeTruthy();
  });
});
