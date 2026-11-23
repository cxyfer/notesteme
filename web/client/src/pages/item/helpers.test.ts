import { describe, expect, it } from "vitest";

import { mapActions } from "./actions";
import { availableModes, defaultMode, stageConfig, targetLangs } from "./data";
import { bilibiliEmbedUrl, bilibiliIds, parseYouTubeMessage, youtubeEmbedUrl } from "./Player";
import { parseChunks } from "./RerunDialog";
import { validateItemSearch } from "./search";
import { run, stage } from "../../test/fixtures";

describe("Player 的網址與訊息", () => {
  it("YouTube 嵌入網址帶 enablejsapi、origin 與 start；影片 ID 會被編碼", () => {
    const u = new URL(youtubeEmbedUrl("a/b", 65.9, true, "http://localhost:8080"));
    expect(u.origin).toBe("https://www.youtube-nocookie.com");
    expect(u.pathname).toBe("/embed/a%2Fb");
    expect(u.searchParams.get("origin")).toBe("http://localhost:8080");
    expect(u.searchParams.get("start")).toBe("65");
    expect(u.searchParams.get("autoplay")).toBe("1");
  });

  it("Bilibili 的 external_id 可能帶 _p<n>；part 欄位優先", () => {
    expect(bilibiliIds("BV1xx411c7mD_p3")).toEqual({ bvid: "BV1xx411c7mD", p: 3 });
    expect(bilibiliIds("BV1xx411c7mD", 2)).toEqual({ bvid: "BV1xx411c7mD", p: 2 });
    const u = new URL(bilibiliEmbedUrl("BV1xx411c7mD_p3", undefined, 10, false));
    expect(u.searchParams.get("bvid")).toBe("BV1xx411c7mD");
    expect(u.searchParams.get("p")).toBe("3");
    expect(u.searchParams.get("t")).toBe("10");
  });

  it("只接受格式正確的 YouTube 訊息", () => {
    expect(parseYouTubeMessage('{"event":"infoDelivery","info":{"currentTime":3.5}}')).toEqual({
      ready: true,
      time: 3.5,
    });
    expect(parseYouTubeMessage('{"event":"onReady"}')).toEqual({ ready: true });
    expect(parseYouTubeMessage("not json")).toBeUndefined();
    expect(parseYouTubeMessage('{"event":"infoDelivery","info":{"currentTime":"x"}}')).toEqual({
      ready: true,
    });
    expect(parseYouTubeMessage({ event: "other" })).toBeUndefined();
  });
});

describe("actions 的對應", () => {
  it("已知的字串對應到動作與 i18n key；重複與空白丟掉；認不得的保留原文", () => {
    expect(mapActions(["上傳 cookie", " 上傳 cookie ", "", "改用 API 處理", "新的動作"])).toEqual([
      { raw: "上傳 cookie", key: "uploadCookie", action: { kind: "uploadCookie" } },
      { raw: "改用 API 處理", key: "switchToApi", action: { kind: "switchToApi" } },
      { raw: "新的動作", action: { kind: "unknown" } },
    ]);
    expect(mapActions(null)).toEqual([]);
    expect(mapActions(["設定 API key"])[0]!.action).toEqual({
      kind: "settings",
      section: "services",
    });
  });
});

describe("parseChunks", () => {
  it("接受逗號、空白、全形逗號與範圍；去重複並排序", () => {
    expect(parseChunks("5-7, 2，2 9")).toEqual([2, 5, 6, 7, 9]);
    expect(parseChunks("")).toEqual([]);
  });
  it("格式不對或範圍不合理時回傳 null", () => {
    expect(parseChunks("0")).toBeNull();
    expect(parseChunks("3-1")).toBeNull();
    expect(parseChunks("a")).toBeNull();
    expect(parseChunks("1-5000")).toBeNull();
  });
});

describe("validateItemSearch", () => {
  it("只留認得的分頁、模式與合理的時間", () => {
    expect(validateItemSearch({ tab: "runs", mode: "diff", t: "65" })).toEqual({
      tab: "runs",
      mode: "diff",
      t: 65,
    });
    expect(validateItemSearch({ tab: "subtitles", mode: "x", t: -1 })).toEqual({});
    expect(validateItemSearch({ t: "NaN" })).toEqual({});
  });
});

describe("詳情頁的資料推導", () => {
  it("可用的模式依步驟狀態；校正完成才預設選校正", () => {
    const modes = availableModes(
      [stage("transcript", "done"), stage("normalize", "done"), stage("correct", "running")],
      false,
    );
    expect(modes).toEqual(["source", "normalized", "corrected", "diff"]);
    // 校正處理中：corrected 可能還不存在，預設用正規化
    expect(defaultMode(modes, [stage("correct", "running")])).toBe("normalized");
    expect(defaultMode(modes, [stage("correct", "done")])).toBe("corrected");
    expect(availableModes([stage("correct", "skipped")], true)).toEqual(["edited"]);
  });

  it("目標語言與步驟的設定來自執行紀錄的設定快照", () => {
    const r = run("correct", {
      config: {
        language: { targets: ["en", 3] },
        stages: { correct: { provider: "p", model: "" } },
      },
    });
    expect(targetLangs([r])).toEqual(["en"]);
    expect(stageConfig(r)).toEqual({
      provider: "p",
      executor: undefined,
      model: undefined,
      prompt: undefined,
    });
    expect(stageConfig(run("x", { config: null }))).toEqual({});
  });
});
