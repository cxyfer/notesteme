import { describe, expect, it } from "vitest";

import { detectLanguage, formatNumber, formatTimecode, matchLanguage } from "./index";

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
});
