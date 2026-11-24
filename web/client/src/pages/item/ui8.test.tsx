import { act, fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes, detail, item, stage } from "../../test/fixtures";
import { mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { chapterTarget } from "./playKeys";

type Detail = ReturnType<typeof detail>;

function open(over: Partial<Detail>) {
  mockApi({
    ...defaultRoutes(),
    "GET /api/items/{id}": () => ({
      ...detail(item(1), [stage("metadata", "done")]),
      ...over,
    }),
  });
  signIn();
  return renderRoute("/items/item-1");
}

describe("影音詳情：從 bundle 匯入（BND N24）", () => {
  it("顯示來源實例、版本與匯入時間；不可信字串當純文字（axe 無違規）", async () => {
    const { container } = open({
      imported_from: {
        instance_id: "<img src=x onerror=alert(1)>",
        app_version: "0.4.0",
        bundle_sha256: "ab".repeat(32),
        imported_at: "2026-11-20T08:00:00Z",
      },
    });
    expect(await screen.findByText("從 bundle 匯入")).toBeTruthy();
    expect(
      screen.getByText(/實例 <img src=x onerror=alert\(1\)>（Notesteme 0\.4\.0）/),
    ).toBeTruthy();
    expect(container.querySelector("img[src='x']")).toBeNull();
    await expectNoA11yViolations(container);
  });

  it("實例 ID 是空字串時寫「不明」", async () => {
    open({
      imported_from: {
        instance_id: "",
        app_version: "0.4.0",
        bundle_sha256: "",
        imported_at: "2026-11-20T08:00:00Z",
      },
    });
    expect(await screen.findByText(/實例 不明/)).toBeTruthy();
  });

  it("imported_from 為 null 時沒有這一欄", async () => {
    open({ imported_from: null });
    await screen.findByRole("heading", { level: 1, name: "影片 1" });
    expect(screen.queryByText("從 bundle 匯入")).toBeNull();
  });
});

describe("影音詳情：播放快捷鍵（README §3.6，UI-8）", () => {
  const chapters = [
    { title: "開場", start: "00:00:00.000", end: "00:01:00.000", origin: "llm" as const },
    { title: "第二章", start: "00:01:00.000", end: "00:05:00.000", origin: "llm" as const },
  ];

  it("[／] 的目標：離章節開頭超過 2 秒先回到開頭，否則上一章", () => {
    const starts = [0, 60, 300];
    expect(chapterTarget(starts, 100, 1)).toBe(300);
    expect(chapterTarget(starts, 100, -1)).toBe(60);
    expect(chapterTarget(starts, 61, -1)).toBe(0);
    expect(chapterTarget(starts, 1, -1)).toBeUndefined();
    expect(chapterTarget(starts, 300, 1)).toBeUndefined();
  });

  it("本機檔案：Space 播放或暫停、←／→ 移動 5 秒、] 跳到下一章；焦點在按鈕上時不接手", async () => {
    const { container } = open({ platform: "local", chapters });
    await screen.findByRole("heading", { level: 1, name: "影片 1" });
    const video = container.querySelector("video")!;
    const play = vi.spyOn(video, "play").mockResolvedValue();
    const pause = vi.spyOn(video, "pause").mockImplementation(() => {});

    fireEvent.keyDown(document.body, { key: " " });
    expect(play).toHaveBeenCalledTimes(1);
    Object.defineProperty(video, "paused", { configurable: true, value: false });
    fireEvent.keyDown(document.body, { key: " " });
    expect(pause).toHaveBeenCalledTimes(1);

    video.currentTime = 10;
    fireEvent.keyDown(document.body, { key: "ArrowRight" });
    expect(video.currentTime).toBe(15);
    fireEvent.keyDown(document.body, { key: "ArrowLeft" });
    fireEvent.keyDown(document.body, { key: "ArrowLeft" });
    fireEvent.keyDown(document.body, { key: "ArrowLeft" });
    expect(video.currentTime).toBe(0);

    fireEvent.keyDown(document.body, { key: "]" });
    expect(video.currentTime).toBe(60);

    // 按鈕上的 Space 是按下那個按鈕
    const button = screen.getByRole("button", { name: "重新處理…" });
    fireEvent.keyDown(button, { key: " " });
    expect(play).toHaveBeenCalledTimes(2); // ] 跳章節時開始播放；Space 沒有再呼叫
    expect(pause).toHaveBeenCalledTimes(1);
  });

  it("YouTube：播放器回應後 Space 送 pauseVideo／playVideo；Bilibili 不接 Space（不擋捲動）", async () => {
    const { container, unmount } = open({ platform: "youtube", external_id: "abcdEFGhijk" });
    await screen.findByRole("heading", { level: 1, name: "影片 1" });
    const frame = container.querySelector("iframe")!;
    const post = vi.spyOn(frame.contentWindow!, "postMessage");
    // 還沒有回應：不處理
    expect(fireEvent.keyDown(document.body, { key: " " })).toBe(true);
    act(() => {
      window.dispatchEvent(
        new MessageEvent("message", {
          origin: "https://www.youtube-nocookie.com",
          source: frame.contentWindow,
          data: JSON.stringify({ event: "infoDelivery", info: { currentTime: 3, playerState: 1 } }),
        }),
      );
    });
    expect(fireEvent.keyDown(document.body, { key: " " })).toBe(false);
    const funcs = post.mock.calls.map(
      (c) => (JSON.parse(c[0] as string) as { func?: string }).func,
    );
    expect(funcs).toContain("pauseVideo");
    unmount();

    open({ platform: "bilibili", external_id: "BV1xx411c7mD" });
    await screen.findByRole("heading", { level: 1, name: "影片 1" });
    expect(fireEvent.keyDown(document.body, { key: " " })).toBe(true);
  });
});
