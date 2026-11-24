// 指令面板（docs/ui/README.md §3.1，UI-8）：Ctrl K／⌘ K 開啟，lazy 載入。
//
//   - 影音：標題搜尋（GET /api/items?q=，和 ItemPicker 共用快取）；另有「在字幕內容中搜尋」導到 /search（不在面板裡重做結果）。
//   - 頁面：導覽的所有頁面與垃圾桶、字幕搜尋。
//   - 動作：新增、暫停佇列或繼續處理（依 GET /api/queue 的狀態只出現一個；佇列沒有啟用時不出現）、更新 yt-dlp。
//
// 鍵盤：輸入框是 combobox，↑／↓ 移動、Enter 執行、Esc 關閉（Radix Dialog）；選項不接焦點（aria-activedescendant）。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useId, useMemo, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

import { useAddDrawer } from "../add/AddContext";
import { api } from "../api/client";
import { describeError } from "../api/describeError";
import { queryKeys } from "../api/queryKeys";
import { Dialog, useToast } from "../components";
import "../styles/ui8.css";

/** 面板裡的頁面（label 是 i18n key） */
const PAGES = [
  ["/", "nav.overview"],
  ["/library", "nav.library"],
  ["/queue", "nav.queue"],
  ["/subscriptions", "nav.subscriptions"],
  ["/search", "pages.search.title"],
  ["/trash", "trash.title"],
  ["/prompts", "nav.prompts"],
  ["/profiles", "nav.profiles"],
  ["/styles", "nav.styles"],
  ["/note-templates", "nav.noteTemplates"],
  ["/costs", "nav.costs"],
  ["/settings", "nav.settings"],
] as const;

/** 和 ItemPicker 相同的查詢（同一個 query key 要是同一種資料） */
const PICKER_LIMIT = 50;
const SHOW_ITEMS = 6;

interface Command {
  id: string;
  group: "items" | "pages" | "actions";
  label: string;
  /** 和標題分開顯示的補充（影音代號） */
  hint?: string;
  run: () => void;
}

export function CommandPalette({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const add = useAddDrawer();
  const qc = useQueryClient();
  const toast = useToast();
  const id = useId();
  const [text, setText] = useState("");
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);

  // 打字中延遲 200ms 再查詢影音
  useEffect(() => {
    const h = setTimeout(() => setQ(text.trim().slice(0, 200)), 200);
    return () => clearTimeout(h);
  }, [text]);

  const items = useQuery({
    queryKey: queryKeys.items.picker(q),
    queryFn: ({ signal }) =>
      api.get("/api/items", { query: { limit: PICKER_LIMIT, q: q || undefined }, signal }),
    enabled: q !== "",
    staleTime: 60_000,
  });
  const queue = useQuery({
    queryKey: queryKeys.queue,
    queryFn: ({ signal }) => api.get<"/api/queue">("/api/queue", { signal }),
    staleTime: 5_000,
    retry: false,
  });

  const commands = useMemo<Command[]>(() => {
    const go = (to: string, search?: Record<string, string>) => () => void navigate({ to, search });
    const run = (fn: () => Promise<unknown>) => () => {
      void fn().catch((err: unknown) => toast.show({ title: describeError(t, err) }));
    };
    const needle = text.trim().toLowerCase();
    const match = (label: string) => !needle || label.toLowerCase().includes(needle);
    const out: Command[] = [];

    if (q) {
      for (const it of (items.data?.items ?? []).slice(0, SHOW_ITEMS))
        out.push({
          id: `item-${it.id}`,
          group: "items",
          label: it.title || it.ref,
          hint: it.ref,
          run: () => void navigate({ to: "/items/$id", params: { id: it.id } }),
        });
      out.push({
        id: "fulltext",
        group: "items",
        label: t("search.fromLibraryQ", { q }),
        run: go("/search", { q }),
      });
    }

    for (const [to, key] of PAGES) {
      const label = t(key);
      if (match(label)) out.push({ id: `page-${to}`, group: "pages", label, run: go(to) });
    }

    const actions: Command[] = [
      { id: "add", group: "actions", label: t("add.open"), run: () => add.open() },
    ];
    const paused = queue.data?.paused;
    if (paused !== undefined)
      actions.push({
        id: "queue",
        group: "actions",
        label: paused ? t("queue.resumeAll") : t("queue.pauseAll"),
        run: run(async () => {
          const r = await api.post(paused ? "/api/queue/resume" : "/api/queue/pause", {
            body: {},
          });
          if (r) qc.setQueryData(queryKeys.queue, r);
          else await qc.invalidateQueries({ queryKey: queryKeys.queue });
          toast.show({ title: paused ? t("queue.resumedAll") : t("queue.pausedAll") });
        }),
      });
    actions.push({
      id: "ytdlp",
      group: "actions",
      label: t("actions.updateYtDlp"),
      run: run(async () => {
        const r = await api.post("/api/tools/yt-dlp/update", { body: {} });
        toast.show({
          title: r?.rolled_back
            ? t("item.actions.ytdlpRolledBack", { version: r.version })
            : t("item.actions.ytdlpUpdated", { version: r?.version ?? "" }),
        });
      }),
    });
    out.push(...actions.filter((a) => match(a.label)));
    return out;
  }, [text, q, items.data, queue.data, t, navigate, add, qc, toast]);

  // 結果改變時，目前的選項回到第一個
  const [prevCount, setPrevCount] = useState(commands.length);
  if (prevCount !== commands.length) {
    setPrevCount(commands.length);
    setActive(0);
  }
  const current = Math.min(active, commands.length - 1);
  const optionId = (i: number) => `${id}-opt-${i}`;

  useEffect(() => {
    document.getElementById(optionId(current))?.scrollIntoView?.({ block: "nearest" });
  });

  function exec(c: Command | undefined) {
    if (!c) return;
    onClose();
    c.run();
  }

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (!commands.length) return;
    const n = commands.length;
    const next = {
      ArrowDown: (current + 1) % n,
      ArrowUp: (current - 1 + n) % n,
      Home: 0,
      End: n - 1,
    }[e.key];
    if (next !== undefined) {
      e.preventDefault();
      setActive(next);
    } else if (e.key === "Enter") {
      e.preventDefault();
      exec(commands[current]);
    }
  }

  const groups = (["items", "pages", "actions"] as const).filter((g) =>
    commands.some((c) => c.group === g),
  );

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title={t("palette.title")}>
      <input
        className="field__input palette__input"
        type="text"
        role="combobox"
        aria-label={t("palette.input")}
        placeholder={t("palette.input")}
        aria-expanded={commands.length > 0}
        aria-controls={`${id}-list`}
        aria-autocomplete="list"
        aria-activedescendant={commands.length ? optionId(current) : undefined}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
        autoComplete="off"
        spellCheck={false}
      />
      <ul
        id={`${id}-list`}
        className="palette__list"
        role="listbox"
        aria-label={t("palette.title")}
      >
        {groups.map((g) => (
          <li key={g} role="presentation">
            <p className="palette__group" id={`${id}-g-${g}`}>
              {t(`palette.groups.${g}`)}
            </p>
            <ul role="group" aria-labelledby={`${id}-g-${g}`}>
              {commands.map((c, i) =>
                c.group !== g ? null : (
                  <li
                    key={c.id}
                    id={optionId(i)}
                    role="option"
                    aria-selected={i === current}
                    className="palette__option"
                    onMouseMove={() => i !== current && setActive(i)}
                    // 不讓輸入框失去焦點
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => exec(c)}
                  >
                    <span>{c.label}</span>
                    {c.hint && <span className="palette__hint secondary">{c.hint}</span>}
                  </li>
                ),
              )}
            </ul>
          </li>
        ))}
      </ul>
      {commands.length === 0 && <p className="secondary">{t("palette.empty")}</p>}
      {q !== "" && items.isFetching && (
        <p className="secondary" role="status">
          {t("common.loading")}
        </p>
      )}
    </Dialog>
  );
}
