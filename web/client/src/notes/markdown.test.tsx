import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { NOTE_MD } from "../test/m4Fixtures";
import { parseInline, parseMarkdown, safeHref, stripFrontmatter } from "./markdown";
import { NoteMarkdown, timeLinkSeconds } from "./NoteMarkdown";

describe("筆記 Markdown：解析", () => {
  it("只允許 http、https 與站內路徑的連結（NFR-SEC-2）", () => {
    expect(safeHref("https://a.test/x")).toBe("https://a.test/x");
    expect(safeHref("http://a.test/x?y=1#z")).toBe("http://a.test/x?y=1#z");
    expect(safeHref("HTTPS://A.test/x")).toBe("https://a.test/x");
    expect(safeHref("  https://a.test/x \n")).toBe("https://a.test/x");
    // 站內路徑原樣保留（以前沒有 base，`new URL` 會丟掉它們）
    expect(safeHref("/items/yt:abc")).toBe("/items/yt:abc");
    expect(safeHref("/api/items/1/note?format=md#top")).toBe("/api/items/1/note?format=md#top");
    expect(safeHref("/%2F%2Fevil.example")).toBe("/%2F%2Fevil.example");
    // mailto: 不在 NFR-SEC-2 的允許清單（修改前會放行）
    expect(safeHref("mailto:a@b.test")).toBeUndefined();
  });

  it("XSS 測試字串一律不產生連結", () => {
    const attacks = [
      "javascript:alert(1)",
      "JavaScript:alert(1)",
      "JAVASCRIPT:alert(1)",
      " javascript:alert(1)",
      "\njavascript:alert(1)",
      "java\tscript:alert(1)",
      "java\nscript:alert(1)",
      "java\rscript:alert(1)",
      "java\u0000script:alert(1)",
      "\u0001javascript:alert(1)",
      "javascript\u007f:alert(1)",
      "javascript&colon;alert(1)",
      "javascript&#58;alert(1)",
      "javascript%3Aalert(1)",
      "%6Aavascript:alert(1)",
      "vbscript:msgbox(1)",
      "VBScript:msgbox(1)",
      "data:text/html,<script>alert(1)</script>",
      "data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==",
      "file:///etc/passwd",
      "obsidian://open",
      "blob:https://a.test/x",
      "//evil.example",
      "//evil.example/x",
      "///evil.example",
      "\\\\evil.example",
      "/\\evil.example",
      "\\/evil.example",
      "https:\\\\evil.example",
      "https://a.test\\@evil.example",
      "https://a.test/\u2028x",
      "/x\ny",
      "#top",
      "?q=1",
      "./x",
      "../x",
      "x/y",
      "",
      "   ",
    ];
    for (const a of attacks) expect(safeHref(a), JSON.stringify(a)).toBeUndefined();
    expect(parseInline("[x](javascript:alert(1))")).toEqual([
      { t: "text", v: "x" },
      { t: "text", v: ")" },
    ]);
    for (const a of [
      "JAVASCRIPT:void0",
      "data:text/html,x",
      "//evil.example",
      "/\\evil",
      "mailto:a@b.test",
    ])
      expect(parseInline(`[x](${a})`)).toEqual([{ t: "text", v: "x" }]);
    expect(parseInline("[站內](/library)")).toEqual([
      { t: "link", href: "/library", c: [{ t: "text", v: "站內" }] },
    ]);
  });

  it("wikilink 只留文字；圖片語法不載入外部圖片", () => {
    expect(parseInline("見 [[頁面|別名]]")).toEqual([
      { t: "text", v: "見 " },
      { t: "text", v: "別名" },
    ]);
    expect(parseInline("![alt](https://evil.test/x.png)")).toEqual([{ t: "text", v: "alt" }]);
    // 圖片只來自 resolveFrame（站內的畫面產出）：`![](/…)` 也只留文字
    expect(parseInline("![alt](/api/items/1/frames/1)")).toEqual([{ t: "text", v: "alt" }]);
  });

  it("去掉 frontmatter；認得標題、表格、callout、清單、嵌入、程式碼", () => {
    const blocks = parseMarkdown(stripFrontmatter(NOTE_MD));
    expect(blocks.map((b) => b.t)).toEqual([
      "heading",
      "table",
      "heading",
      "para",
      "embed",
      "para",
      "callout",
      "heading",
      "list",
    ]);
    expect(parseMarkdown("```dataviewjs\nalert(1)\n```")).toEqual([
      { t: "code", lang: "dataviewjs", v: "alert(1)" },
    ]);
  });
});

describe("筆記 Markdown：渲染", () => {
  it("原始 HTML 當成文字；危險連結不產生 <a>；標題往下移一層", () => {
    const { container } = render(<NoteMarkdown markdown={NOTE_MD} />);
    expect(container.querySelector("script")).toBeNull();
    expect(screen.getByText(/<script>alert\(1\)<\/script>/)).toBeTruthy();
    for (const a of container.querySelectorAll("a"))
      expect(a.getAttribute("href")).toMatch(/^https?:/);
    expect(container.querySelector("h1")).toBeNull();
    expect(container.querySelector("h2")?.textContent).toBe("影片 1");
    expect(container.querySelector("table")).toBeTruthy();
  });

  it("站內路徑產生連結；繞過寫法只剩文字", () => {
    const md = [
      "[媒體庫](/library) [外部](https://a.test/x)",
      "[a](JaVaScRiPt:void0) [b](vbscript:x) [c](data:text/html,x) [d](mailto:a@b.test)",
      "[e](//evil.example) [f](/\\evil.example) [g](javascript&colon;x) [h](javascript%3Ax)",
    ].join("\n\n");
    const { container } = render(<NoteMarkdown markdown={md} />);
    expect([...container.querySelectorAll("a")].map((a) => a.getAttribute("href"))).toEqual([
      "/library",
      "https://a.test/x",
    ]);
    expect(screen.getByText(/a b c d/)).toBeTruthy();
  });

  it("同一部影片的時間連結改成跳播；畫面嵌入對應到圖片", () => {
    const onSeek = vi.fn();
    const url = "https://www.youtube.com/watch?v=vid00000001";
    render(
      <NoteMarkdown
        markdown={NOTE_MD}
        onSeek={onSeek}
        onTimeLink={(h) => timeLinkSeconds(url, h)}
        resolveFrame={(n) => ({
          src: `/api/items/item-1/frames/${n}?size=thumb`,
          alt: `畫面 ${n}`,
        })}
      />,
    );
    fireEvent.click(screen.getByRole("link", { name: "00:05" }));
    expect(onSeek).toHaveBeenCalledWith(5);
    expect(screen.getByRole("img", { name: "畫面 1" }).getAttribute("src")).toBe(
      "/api/items/item-1/frames/1?size=thumb",
    );
  });

  it("timeLinkSeconds：只認同一部影音，支援 t=1m5s 與 #t=", () => {
    const yt = "https://www.youtube.com/watch?v=abc";
    expect(timeLinkSeconds(yt, "https://youtube.com/watch?v=abc&t=1m5s")).toBe(65);
    expect(timeLinkSeconds(yt, "https://www.youtube.com/watch?v=other&t=5")).toBeUndefined();
    expect(timeLinkSeconds("https://pod.test/ep.mp3", "https://pod.test/ep.mp3#t=90")).toBe(90);
    expect(timeLinkSeconds(undefined, "https://pod.test/ep.mp3#t=90")).toBeUndefined();
  });
});
