// Obsidian 風格 Markdown 的最小解析器（NOTE-R8：筆記是 LLM 依字幕寫成的不可信內容）。
// 只產生資料結構，由 NoteMarkdown.tsx 轉成 React element：不使用 innerHTML，原始 HTML 一律當成文字。
// 支援：frontmatter（略過）、標題、段落（單一換行即換行）、清單、引用與 callout、程式碼區塊（純文字）、
// 表格、水平線、`![[…]]` 嵌入；行內的粗體、斜體、行內 code、連結、wikilink。

export type Inline =
  | { t: "text"; v: string }
  | { t: "code"; v: string }
  | { t: "strong"; c: Inline[] }
  | { t: "em"; c: Inline[] }
  | { t: "link"; href: string; c: Inline[] }
  | { t: "br" };

export type Block =
  | { t: "heading"; level: number; c: Inline[] }
  | { t: "para"; c: Inline[] }
  | { t: "list"; ordered: boolean; items: Inline[][] }
  | { t: "quote"; blocks: Block[] }
  | { t: "callout"; kind: string; title: Inline[]; blocks: Block[] }
  | { t: "code"; lang: string; v: string }
  | { t: "table"; head: Inline[][]; rows: Inline[][][] }
  | { t: "embed"; target: string }
  | { t: "hr" };

/** 只允許 http、https、mailto；其他（javascript:、data:、file:、obsidian: …）回傳 undefined */
export function safeHref(href: string): string | undefined {
  try {
    const u = new URL(href.trim());
    return u.protocol === "http:" ||
      u.protocol === "https:" ||
      u.protocol === "mailto:"
      ? u.href
      : undefined;
  } catch {
    return undefined;
  }
}

const FENCE = /^(```|~~~)\s*([\w-]*)\s*$/;
const HEADING = /^(#{1,6})\s+(.*?)\s*#*\s*$/;
const LIST = /^\s*([-*+]|\d+[.)])\s+(.*)$/;
const HR = /^\s*([-*_])(\s*\1){2,}\s*$/;
const EMBED = /^\s*!\[\[([^\]]+)\]\]\s*$/;
const TABLE_SEP = /^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?\s*$/;
const CALLOUT = /^\[!([\w-]+)\][+-]?\s*(.*)$/;

function splitRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|")) s = s.slice(0, -1);
  return s.split("|").map((c) => c.trim());
}

function startsBlock(line: string, next: string | undefined): boolean {
  return (
    FENCE.test(line) ||
    HEADING.test(line) ||
    LIST.test(line) ||
    HR.test(line) ||
    EMBED.test(line) ||
    line.trimStart().startsWith(">") ||
    (line.trimStart().startsWith("|") &&
      next !== undefined &&
      TABLE_SEP.test(next))
  );
}

/** 去掉開頭的 frontmatter（API 另外回傳 frontmatter 欄位） */
export function stripFrontmatter(md: string): string {
  const m = /^---\r?\n[\s\S]*?\r?\n---\s*(\r?\n|$)/.exec(md);
  return m ? md.slice(m[0].length) : md;
}

export function parseMarkdown(md: string, depth = 0): Block[] {
  const lines = md.replace(/\r\n?/g, "\n").split("\n");
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i]!;
    if (line.trim() === "") {
      i++;
      continue;
    }
    const fence = FENCE.exec(line);
    if (fence) {
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i]!.trim().startsWith(fence[1]!))
        body.push(lines[i++]!);
      i++;
      out.push({ t: "code", lang: fence[2] ?? "", v: body.join("\n") });
      continue;
    }
    const h = HEADING.exec(line);
    if (h) {
      out.push({ t: "heading", level: h[1]!.length, c: parseInline(h[2]!) });
      i++;
      continue;
    }
    if (HR.test(line)) {
      out.push({ t: "hr" });
      i++;
      continue;
    }
    const embed = EMBED.exec(line);
    if (embed) {
      out.push({ t: "embed", target: embed[1]! });
      i++;
      continue;
    }
    if (line.trimStart().startsWith(">")) {
      const inner: string[] = [];
      while (i < lines.length && lines[i]!.trimStart().startsWith(">"))
        inner.push(lines[i++]!.trimStart().replace(/^>\s?/, ""));
      // 巢狀太深時不再遞迴，當成段落
      if (depth > 8) {
        out.push({ t: "para", c: parseInline(inner.join(" ")) });
        continue;
      }
      const c = CALLOUT.exec(inner[0] ?? "");
      if (c) {
        out.push({
          t: "callout",
          kind: c[1]!.toLowerCase(),
          title: parseInline(c[2] || c[1]!),
          blocks: parseMarkdown(inner.slice(1).join("\n"), depth + 1),
        });
      } else
        out.push({
          t: "quote",
          blocks: parseMarkdown(inner.join("\n"), depth + 1),
        });
      continue;
    }
    if (
      line.trimStart().startsWith("|") &&
      i + 1 < lines.length &&
      TABLE_SEP.test(lines[i + 1]!)
    ) {
      const head = splitRow(line).map(parseInline);
      i += 2;
      const rows: Inline[][][] = [];
      while (i < lines.length && lines[i]!.trimStart().startsWith("|"))
        rows.push(splitRow(lines[i++]!).map(parseInline));
      out.push({ t: "table", head, rows });
      continue;
    }
    const li = LIST.exec(line);
    if (li) {
      const ordered = /\d/.test(li[1]!);
      const items: Inline[][] = [];
      while (i < lines.length) {
        const m = LIST.exec(lines[i]!);
        if (!m) break;
        items.push(parseInline(m[2]!));
        i++;
      }
      out.push({ t: "list", ordered, items });
      continue;
    }
    const para: string[] = [];
    while (i < lines.length && lines[i]!.trim() !== "") {
      if (para.length > 0 && startsBlock(lines[i]!, lines[i + 1])) break;
      para.push(lines[i++]!);
    }
    const c: Inline[] = [];
    para.forEach((p, k) => {
      if (k > 0) c.push({ t: "br" });
      c.push(...parseInline(p.trim()));
    });
    out.push({ t: "para", c });
  }
  return out;
}

// 行內：依序找最早出現的語法；找不到的部分都是文字
const INLINE =
  /`([^`]+)`|!?\[\[([^\]]+)\]\]|!?\[([^\]]*)\]\(([^)\s]*)\)|\*\*(.+?)\*\*|__(.+?)__|\*([^*\s][^*]*?)\*|(?<![\w])_([^_\s][^_]*?)_(?![\w])/;

export function parseInline(s: string, depth = 0): Inline[] {
  const out: Inline[] = [];
  let rest = s;
  while (rest) {
    const m = depth > 6 ? null : INLINE.exec(rest);
    if (!m) {
      out.push({ t: "text", v: rest });
      break;
    }
    if (m.index > 0) out.push({ t: "text", v: rest.slice(0, m.index) });
    const [all, code, wiki, label, href, b1, b2, i1, i2] = m;
    if (code !== undefined) out.push({ t: "code", v: code });
    else if (wiki !== undefined) {
      // wikilink 與嵌入只留文字（別名優先）
      const parts = wiki.split("|");
      out.push({ t: "text", v: (parts[1] ?? parts[0] ?? "").trim() });
    } else if (label !== undefined) {
      const safe = all.startsWith("!") ? undefined : safeHref(href ?? "");
      const c = parseInline(label, depth + 1);
      if (safe) out.push({ t: "link", href: safe, c });
      else out.push(...c);
    } else if (b1 !== undefined || b2 !== undefined)
      out.push({ t: "strong", c: parseInline((b1 ?? b2)!, depth + 1) });
    else out.push({ t: "em", c: parseInline((i1 ?? i2)!, depth + 1) });
    rest = rest.slice(m.index + all.length);
  }
  return out;
}
