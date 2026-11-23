import { describe, expect, it } from "vitest";

import {
  bookmarkletCode,
  MAX_ENTRY_URL_CHARS,
  resolveAddEntry,
  validateAddEntrySearch,
} from "./entry";

describe("resolveAddEntry（ENTRY-R2、R4）", () => {
  it("http／https 的網址帶入抽屜", () => {
    expect(resolveAddEntry({ url: "https://youtu.be/abc123xxxxx" })).toEqual({
      url: "https://youtu.be/abc123xxxxx",
    });
    expect(resolveAddEntry({ url: " http://a.example.com/x " })).toEqual({
      url: "http://a.example.com/x",
    });
  });

  it.each([
    "javascript:alert(1)",
    "data:text/html,<script>alert(1)</script>",
    "file:///etc/passwd",
    "//evil.example.com",
    "https://",
    "https://a.example.com/a b",
    "",
    "https://a.example.com/" + "x".repeat(MAX_ENTRY_URL_CHARS),
  ])("忽略不合格的網址：%s", (url) => {
    expect(resolveAddEntry({ url })).toEqual({ notice: "invalidUrl" });
  });

  it("剛好 2048 個字元（以 code point 計）可以接受", () => {
    const base = "https://a.example.com/";
    const url = base + "𝒳".repeat(MAX_ENTRY_URL_CHARS - base.length);
    expect(resolveAddEntry({ url })).toEqual({ url });
  });

  it("分享沒有網址", () => {
    expect(resolveAddEntry({ share_error: "no_url" })).toEqual({ notice: "noUrl" });
    expect(resolveAddEntry({ share_error: "other" })).toEqual({});
    expect(resolveAddEntry({})).toEqual({});
  });

  it("只接受字串的 query", () => {
    expect(validateAddEntrySearch({ url: 123, share_error: ["no_url"] })).toEqual({});
    expect(validateAddEntrySearch({ url: "https://x", share_error: "no_url", x: 1 })).toEqual({
      url: "https://x",
      share_error: "no_url",
    });
  });
});

describe("bookmarkletCode（ENTRY-R1）", () => {
  it("只讀 location.href 並在新分頁開啟 /add", () => {
    expect(bookmarkletCode("https://n.example.com")).toBe(
      "javascript:(()=>{window.open('https://n.example.com/add?url='+encodeURIComponent(location.href),'_blank','noopener')})()",
    );
  });

  it("不含憑證，也不讀頁面內容", () => {
    const code = bookmarkletCode("http://localhost:8080");
    for (const s of [
      "cookie",
      "token",
      "document",
      "fetch",
      "XMLHttpRequest",
      "localStorage",
      "title",
      "getSelection",
    ]) {
      expect(code).not.toContain(s);
    }
  });
});
