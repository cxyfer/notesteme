import { describe, expect, it } from "vitest";

import { readCookie, safeRedirect } from "./session";

describe("safeRedirect", () => {
  it.each([
    ["/queue?x=1", "/queue?x=1"],
    ["/items/yt%3Aabc", "/items/yt%3Aabc"],
    [undefined, "/library"],
    ["", "/library"],
    ["https://evil.example/", "/library"],
    ["//evil.example", "/library"],
    ["/\\evil.example", "/library"],
    ["javascript:alert(1)", "/library"],
    ["/a\nb", "/library"],
    ["/login", "/library"],
    ["/login?redirect=/x", "/library"],
    [42, "/library"],
  ])("%s → %s", (input, want) => {
    expect(safeRedirect(input)).toBe(want);
  });
});

describe("readCookie", () => {
  it("讀取並解碼", () => {
    expect(readCookie("notesteme_csrf", "a=1; notesteme_csrf=x%3Dy; b=2")).toBe("x=y");
    expect(readCookie("missing", "a=1")).toBeUndefined();
  });
});
