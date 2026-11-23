import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { NOTE_MD } from "../test/m4Fixtures";
import { parseInline, parseMarkdown, safeHref, stripFrontmatter } from "./markdown";
import { NoteMarkdown, timeLinkSeconds } from "./NoteMarkdown";

describe("筆記 Markdown：解析", () => {
  it("只允許 http、https、mailto 的連結", () => {
    expect(safeHref("https://a.test/x")).toBe("https://a.test/x");
    expect(safeHref("mailto:a@b.test")).toBe("mailto:a@b.test");
    expect(safeHref("javascript:alert(1)")).toBeUndefined();
    expect(safeHref("data:text/html,x")).toBeUndefined();
    expect(safeHref("obsidian://open")).toBeUndefined();
    expect(parseInline("[x](javascript:alert(1))")).toEqual([
      { t: "text", v: "x" },
      { t: "text", v: ")" },
    ]);
  });

  it("wikilink 只留文字；圖片語法不載入外部圖片", () => {
    expect(parseInline("見 [[頁面|別名]]")).toEqual([
      { t: "text", v: "見 " },
      { t: "text", v: "別名" },
    ]);
    expect(parseInline("![alt](https://evil.test/x.png)")).toEqual([{ t: "text", v: "alt" }]);
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
