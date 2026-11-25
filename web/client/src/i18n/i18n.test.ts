import { describe, expect, it, vi } from "vitest";

import { detectLanguage, formatDate, formatNumber, formatTimecode, matchLanguage } from "./index";

describe("語言", () => {
  it.each([
    ["zh-TW", "zh-Hant"],
    ["zh-HK", "zh-Hant"],
    ["zh-Hant-TW", "zh-Hant"],
    ["zh-CN", "zh-Hans"],
    ["zh-Hans", "zh-Hans"],
    ["zh-SG", "zh-Hans"],
    ["zh", "zh-Hant"],
    ["en-US", "en"],
    ["ja", undefined],
  ])("matchLanguage(%s) = %s", (tag, want) => {
    expect(matchLanguage(tag)).toBe(want);
  });

  it("使用者選過的優先，其次是瀏覽器偏好，最後是繁中", () => {
    expect(detectLanguage("en", ["zh-CN"])).toBe("en");
    expect(detectLanguage(null, ["ja", "zh-CN"])).toBe("zh-Hans");
    expect(detectLanguage("fr", ["ja"])).toBe("zh-Hant");
  });
});

describe("格式化", () => {
  it("數字用 Intl，時間碼不隨語言改變", () => {
    expect(formatNumber(12345.5, "en")).toBe("12,345.5");
    expect(formatTimecode(65)).toBe("01:05");
    expect(formatTimecode(3725)).toBe("1:02:05");
    expect(formatTimecode(-3)).toBe("00:00");
  });

  it("同一語言與選項的 Intl 格式器只建立一次（NFR-PERF-10：媒體庫捲動時每一列都格式化日期）", () => {
    const spy = vi.spyOn(Intl, "DateTimeFormat");
    try {
      for (let i = 0; i < 50; i++) formatDate("2026-11-01T00:00:00Z", "en");
      expect(spy.mock.calls.length).toBeLessThanOrEqual(1);
      // 快取依語言區分：換語言仍是該語言的格式
      expect(formatDate("2026-11-01T00:00:00Z", "en")).not.toBe(
        formatDate("2026-11-01T00:00:00Z", "zh-Hant"),
      );
    } finally {
      spy.mockRestore();
    }
  });
});
