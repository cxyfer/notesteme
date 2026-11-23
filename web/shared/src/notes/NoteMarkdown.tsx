// 把 markdown.ts 的結構渲染成 React element（閱讀字體：.note-view 的樣式由使用端提供，Client 在 styles/m4.css）。
// 標題層級預設往下移一層（頁面已有 h1）。時間連結可以交給 onTimeLink 讓播放器跳轉；
// `![[…frame_NNNN.jpg]]` 由 resolveFrame 對應到畫面圖片，其他嵌入只顯示名稱。
// M5-S9 從 web/client/src/notes 搬到 web/shared，Hub UI 也用它渲染筆記（HREP-R18：不允許原始 HTML）。
import { Fragment, useMemo, type MouseEvent, type ReactNode } from "react";

import {
  parseMarkdown,
  stripFrontmatter,
  type Block,
  type Inline,
} from "./markdown";

export interface NoteMarkdownProps {
  markdown: string;
  lang?: string;
  headingOffset?: number;
  /** 回傳秒數時，點擊連結改成跳播（例如同一部影片的 &t=） */
  onTimeLink?: (href: string) => number | undefined;
  onSeek?: (seconds: number) => void;
  /** frame_NNNN 的編號 → 圖片；沒有時顯示嵌入的名稱 */
  resolveFrame?: (n: number) => { src: string; alt: string } | undefined;
  label?: string;
}

const FRAME = /frame_(\d{1,6})\.(?:jpe?g|png|webp)$/i;

export function NoteMarkdown({
  markdown,
  lang,
  headingOffset = 1,
  onTimeLink,
  onSeek,
  resolveFrame,
  label,
}: NoteMarkdownProps) {
  const blocks = useMemo(
    () => parseMarkdown(stripFrontmatter(markdown)),
    [markdown],
  );
  const ctx: Ctx = { headingOffset, onTimeLink, onSeek, resolveFrame };
  return (
    <div
      className="note-view"
      lang={lang}
      aria-label={label}
      role={label ? "region" : undefined}
    >
      {renderBlocks(blocks, ctx)}
    </div>
  );
}

interface Ctx {
  headingOffset: number;
  onTimeLink?: (href: string) => number | undefined;
  onSeek?: (seconds: number) => void;
  resolveFrame?: (n: number) => { src: string; alt: string } | undefined;
}

function renderBlocks(blocks: Block[], ctx: Ctx): ReactNode {
  return blocks.map((b, i) => (
    <Fragment key={i}>{renderBlock(b, ctx)}</Fragment>
  ));
}

function renderBlock(b: Block, ctx: Ctx): ReactNode {
  switch (b.t) {
    case "heading": {
      const level = Math.min(6, b.level + ctx.headingOffset);
      const Tag = `h${level}` as "h2";
      return <Tag>{renderInline(b.c, ctx)}</Tag>;
    }
    case "para":
      return <p>{renderInline(b.c, ctx)}</p>;
    case "list": {
      const items = b.items.map((it, i) => (
        <li key={i}>{renderInline(it, ctx)}</li>
      ));
      return b.ordered ? <ol>{items}</ol> : <ul>{items}</ul>;
    }
    case "quote":
      return <blockquote>{renderBlocks(b.blocks, ctx)}</blockquote>;
    case "callout":
      return (
        <div className="callout-block" data-kind={b.kind}>
          <p className="callout-block__title">{renderInline(b.title, ctx)}</p>
          {renderBlocks(b.blocks, ctx)}
        </div>
      );
    case "code":
      return (
        <pre>
          <code>{b.v}</code>
        </pre>
      );
    case "table":
      return (
        <table>
          <thead>
            <tr>
              {b.head.map((c, i) => (
                <th key={i} scope="col">
                  {renderInline(c, ctx)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {b.rows.map((r, i) => (
              <tr key={i}>
                {r.map((c, j) => (
                  <td key={j}>{renderInline(c, ctx)}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      );
    case "embed": {
      const m = FRAME.exec(b.target.split("|")[0]!.trim());
      const img = m ? ctx.resolveFrame?.(Number(m[1])) : undefined;
      if (img)
        return (
          <figure>
            <img src={img.src} alt={img.alt} loading="lazy" />
          </figure>
        );
      return <p className="note-view__embed">{b.target}</p>;
    }
    case "hr":
      return <hr />;
  }
}

function renderInline(c: Inline[], ctx: Ctx): ReactNode {
  return c.map((n, i) => {
    switch (n.t) {
      case "text":
        return <Fragment key={i}>{n.v}</Fragment>;
      case "code":
        return <code key={i}>{n.v}</code>;
      case "strong":
        return <strong key={i}>{renderInline(n.c, ctx)}</strong>;
      case "em":
        return <em key={i}>{renderInline(n.c, ctx)}</em>;
      case "br":
        return <br key={i} />;
      case "link": {
        const s = ctx.onSeek ? ctx.onTimeLink?.(n.href) : undefined;
        const onClick =
          s !== undefined
            ? (e: MouseEvent) => {
                e.preventDefault();
                ctx.onSeek?.(s);
              }
            : undefined;
        return (
          <a
            key={i}
            href={n.href}
            target={onClick ? undefined : "_blank"}
            rel="noreferrer noopener"
            onClick={onClick}
          >
            {renderInline(n.c, ctx)}
          </a>
        );
      }
    }
  });
}

/**
 * 筆記的時間連結（NOTE 的 render：YouTube `&t=`、Bilibili `?p=&t=`、Podcast `#t=`）是否指向這部影音；
 * 是的話回傳秒數。比對主機、路徑與 YouTube 的 v。
 */
export function timeLinkSeconds(
  itemUrl: string | undefined,
  href: string,
): number | undefined {
  if (!itemUrl) return undefined;
  try {
    const a = new URL(itemUrl);
    const b = new URL(href);
    const key = (u: URL) =>
      `${u.hostname.replace(/^www\./, "")}${u.pathname}|${u.searchParams.get("v") ?? ""}`;
    if (key(a) !== key(b)) return undefined;
    const raw =
      b.searchParams.get("t") ?? /(?:^|&)t=([^&]+)/.exec(b.hash.slice(1))?.[1];
    if (!raw) return undefined;
    const m = /^(?:(\d+)h)?(?:(\d+)m)?(\d+(?:\.\d+)?)s?$/.exec(raw);
    if (!m) return undefined;
    const s = Number(m[1] ?? 0) * 3600 + Number(m[2] ?? 0) * 60 + Number(m[3]);
    return Number.isFinite(s) ? s : undefined;
  } catch {
    return undefined;
  }
}
