// 字幕句列表（design-language.md §6.2、LIB-R5～R7、R12）：虛擬化，長片數千句也只渲染可視範圍。
//
//   - 點時間碼跳到播放器的時間；目前播放的句子左側有 Cue 色條（另有文字，不只靠顏色）。
//   - 顯示模式：原始、正規化、校正、差異（逐字）、譯文、對照、已編輯。切換模式時記住可視範圍第一句的時間，
//     新的字幕到了再捲回同一個時間，捲動位置不變（LIB-R5）。
//   - 字幕是不可信輸入：只當純文字渲染；元素設定字幕自己的 lang（NFR-I18N-3）。
//   - 播放時自動捲動，讓目前句保持在視窗中間；使用者手動捲動後停止，顯示「回到目前位置」。
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type ReactNode,
} from "react";
import { useTranslation } from "react-i18next";

import {
  Button,
  ErrorCallout,
  Spinner,
  VirtualList,
  type RowFocusProps,
  type VirtualListHandle,
} from "../../components";
import { formatNumber, formatTimecode } from "../../i18n";
import { alignCues, cueAt, joinText, type Cue } from "./cues";
import { modeVariant, useCues, useDiffBase, type CueSet } from "./data";
import { diffChars, hasChanges, type DiffPart } from "./diff";
import type { CueMode } from "./search";

const CENTER = "center";
/** 時間不是來源提供的句子，時間碼旁的空心小圓點（design-language.md §6.2） */
const TIMING_MARK = "◦";

export type CueRow =
  | { kind: "single"; key: string; start: number; end: number; cue: Cue; lang: string }
  | {
      kind: "diff";
      key: string;
      start: number;
      end: number;
      parts: DiffPart[];
      /** 這一段還沒有校正結果（校正處理中） */
      pending: boolean;
      changed: boolean;
      lang: string;
      timing?: string;
    }
  | {
      kind: "parallel";
      key: string;
      start: number;
      end: number;
      original: string;
      translated: string;
      lang: string;
      targetLang: string;
    };

export function singleRows(set: CueSet): CueRow[] {
  return set.cues.map((cue) => ({
    kind: "single",
    key: `${cue.index}-${cue.start}`,
    start: cue.start,
    end: cue.end,
    cue,
    lang: set.lang,
  }));
}

export function diffRows(base: CueSet, corrected: CueSet | null): CueRow[] {
  const groups = alignCues(base.cues, corrected?.cues ?? []);
  return groups.map((g, i) => {
    const before = joinText(g.a, base.lang);
    const after = joinText(g.b, corrected?.lang ?? base.lang);
    const pending = g.b.length === 0;
    const parts = pending ? [{ type: "eq" as const, text: before }] : diffChars(before, after);
    return {
      kind: "diff",
      key: `g${i}-${g.start}`,
      start: g.start,
      end: g.end,
      parts,
      pending,
      changed: !pending && hasChanges(parts),
      lang: corrected?.lang ?? base.lang,
      timing: (g.b[0] ?? g.a[0])?.timing,
    };
  });
}

export function parallelRows(original: CueSet, translated: CueSet): CueRow[] {
  return alignCues(original.cues, translated.cues).map((g, i) => ({
    kind: "parallel",
    key: `p${i}-${g.start}`,
    start: g.start,
    end: g.end,
    original: joinText(g.a, original.lang),
    translated: joinText(g.b, translated.lang),
    lang: original.lang,
    targetLang: translated.lang,
  }));
}

function rowText(r: CueRow): string {
  switch (r.kind) {
    case "single":
      return r.cue.text;
    case "diff":
      return r.parts.map((p) => p.text).join("");
    case "parallel":
      return `${r.original}\n${r.translated}`;
  }
}

export interface CueListProps {
  itemId: string;
  mode: CueMode;
  modes: readonly CueMode[];
  onModeChange: (mode: CueMode) => void;
  /** 對照模式的原文用哪個變體（校正 > 正規化 > 原始） */
  originalVariant: string;
  /** 翻譯的目標語言（多個時可以選） */
  targetLangs: readonly string[];
  currentTime: number;
  onSeek: (seconds: number) => void;
}

export function CueList({
  itemId,
  mode,
  modes,
  onModeChange,
  originalVariant,
  targetLangs,
  currentTime,
  onSeek,
}: CueListProps) {
  const { t } = useTranslation();
  const [lang, setLang] = useState<string | undefined>(undefined);
  const translatedLang = lang && targetLangs.includes(lang) ? lang : undefined;
  const needsTarget = mode === "translated" || mode === "parallel";

  const primary = useCues(itemId, modeVariant(mode), needsTarget ? translatedLang : undefined);
  const base = useDiffBase(itemId, mode === "diff");
  const original = useCues(itemId, mode === "parallel" ? originalVariant : undefined);

  const rows = useMemo<CueRow[] | undefined>(() => {
    if (mode === "diff") {
      if (!base.data && base.isPending) return undefined;
      if (!base.data) return primary.data ? singleRows(primary.data) : [];
      if (primary.isPending) return undefined;
      return diffRows(base.data, primary.data ?? null);
    }
    if (mode === "parallel") {
      if (primary.isPending || original.isPending) return undefined;
      if (!primary.data || !original.data) return primary.data ? singleRows(primary.data) : [];
      return parallelRows(original.data, primary.data);
    }
    if (primary.isPending) return undefined;
    return primary.data ? singleRows(primary.data) : [];
  }, [
    mode,
    primary.data,
    primary.isPending,
    base.data,
    base.isPending,
    original.data,
    original.isPending,
  ]);

  const loading = primary.isPlaceholderData || base.isPlaceholderData || original.isPlaceholderData;
  const error = primary.error ?? base.error ?? original.error;

  // ---- 搜尋 ----
  const [query, setQuery] = useState("");
  const q = query.trim().toLocaleLowerCase();
  const shown = useMemo(
    () => (rows && q ? rows.filter((r) => rowText(r).toLocaleLowerCase().includes(q)) : rows),
    [rows, q],
  );

  // ---- 目前句與自動捲動 ----
  const listRef = useRef<VirtualListHandle>(null);
  const active = shown ? cueAt(shown, currentTime) : -1;
  const activeRow = shown && active >= 0 && currentTime < shown[active]!.end + 1 ? active : -1;
  const [follow, setFollow] = useState(true);
  useEffect(() => {
    if (follow && activeRow >= 0) listRef.current?.scrollToIndex(activeRow, "center");
  }, [follow, activeRow]);
  const stopFollow = useCallback(() => setFollow(false), []);
  const backToCurrent = () => {
    setFollow(true);
    listRef.current?.scrollToIndex(activeRow, CENTER);
  };

  // ---- 切換模式時保留捲動位置：記住第一句的時間，新的字幕到了再捲回去 ----
  const anchor = useRef<number | null>(null);
  const changeMode = (m: CueMode) => {
    if (m === mode) return;
    const first = listRef.current?.firstVisibleIndex() ?? -1;
    anchor.current = shown && first >= 0 ? shown[first]!.start : null;
    onModeChange(m);
  };
  useEffect(() => {
    if (anchor.current === null || !shown || loading) return;
    const i = Math.max(0, cueAt(shown, anchor.current));
    anchor.current = null;
    listRef.current?.scrollToIndex(i, "start");
  }, [shown, loading]);

  return (
    <section className="cues" aria-labelledby="cues-title">
      <h2 id="cues-title" className="visually-hidden">
        {t("item.cues.title")}
      </h2>
      <div className="cues__bar">
        <div className="segmented" role="radiogroup" aria-label={t("item.cues.mode")}>
          {modes.map((m) => (
            <button
              key={m}
              type="button"
              role="radio"
              aria-checked={m === mode}
              className="segmented__option"
              onClick={() => changeMode(m)}
            >
              {t(`item.cues.modes.${m}`)}
            </button>
          ))}
        </div>
        {needsTarget && targetLangs.length > 1 && (
          <label className="cues__lang">
            <span>{t("item.cues.targetLang")}</span>
            <select
              value={translatedLang ?? ""}
              onChange={(e: ChangeEvent<HTMLSelectElement>) => setLang(e.target.value || undefined)}
            >
              <option value="">{t("item.cues.latestLang")}</option>
              {targetLangs.map((l) => (
                <option key={l} value={l}>
                  {l}
                </option>
              ))}
            </select>
          </label>
        )}
        <label className="cues__search">
          <span className="visually-hidden">{t("item.cues.search")}</span>
          <input
            type="search"
            className="field__input"
            placeholder={t("item.cues.searchPlaceholder")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") setQuery("");
            }}
          />
        </label>
      </div>
      <p className="cues__meta secondary num" aria-live="polite">
        {shown &&
          (q
            ? t("item.cues.matches", { count: shown.length, formatted: formatNumber(shown.length) })
            : t("item.cues.count", { count: shown.length, formatted: formatNumber(shown.length) }))}
        {primary.data?.lang && (
          <span className="cues__langs">
            {needsTarget
              ? t("item.cues.langTarget", { lang: primary.data.lang })
              : t("item.cues.langSource", { lang: primary.data.lang })}
          </span>
        )}
        {loading && <Spinner label={t("item.cues.loading")} />}
      </p>
      {mode === "diff" && <DiffLegend />}

      {error && !rows ? (
        <ErrorCallout
          error={error}
          actions={[
            {
              label: t("common.retry"),
              onClick: () => {
                void primary.refetch();
                if (mode === "diff") void base.refetch();
                if (mode === "parallel") void original.refetch();
              },
            },
          ]}
        />
      ) : !shown ? (
        <Spinner label={t("item.cues.loading")} showLabel />
      ) : shown.length === 0 ? (
        <p className="cues__empty secondary">
          {q ? t("item.cues.noMatches") : t("item.cues.empty")}
        </p>
      ) : (
        <div
          className="cues__wrap"
          onWheel={stopFollow}
          onTouchMove={stopFollow}
          onKeyDown={(e) => {
            if (["ArrowDown", "ArrowUp", "PageDown", "PageUp", "Home", "End"].includes(e.key))
              stopFollow();
          }}
        >
          {!follow && activeRow >= 0 && (
            <Button className="cues__follow" onClick={backToCurrent}>
              {t("item.cues.backToCurrent")}
            </Button>
          )}
          <VirtualList
            ref={listRef}
            className="cues__list"
            label={t("item.cues.listLabel")}
            items={shown}
            getKey={(r) => r.key}
            estimateSize={mode === "parallel" ? 64 : 40}
            overscan={12}
            renderItem={(r, { index, focusProps }) => (
              <CueLine
                row={r}
                current={index === activeRow}
                focusProps={focusProps}
                onSeek={onSeek}
              />
            )}
          />
        </div>
      )}
    </section>
  );
}

function DiffLegend() {
  const { t } = useTranslation();
  return (
    <p className="cues__legend secondary">
      <del className="diff__del">{t("item.cues.diffDeleted")}</del>
      <ins className="diff__ins">{t("item.cues.diffInserted")}</ins>
    </p>
  );
}

function TimingMark({ timing }: { timing?: string }) {
  const { t } = useTranslation();
  if (!timing || timing === "source") return null;
  const label = t(`timing.${timing}`, { defaultValue: timing });
  return (
    <span className="cue__timing" title={label}>
      <span aria-hidden="true">{TIMING_MARK}</span>
      <span className="visually-hidden">{label}</span>
    </span>
  );
}

function CueLine({
  row,
  current,
  focusProps,
  onSeek,
}: {
  row: CueRow;
  current: boolean;
  focusProps: RowFocusProps;
  onSeek: (s: number) => void;
}) {
  const { t } = useTranslation();
  const time = formatTimecode(row.start);
  let body: ReactNode;
  switch (row.kind) {
    case "single":
      body = (
        <p className="cue__text" lang={row.lang || undefined}>
          {row.cue.text}
        </p>
      );
      break;
    case "diff":
      body = (
        <p className="cue__text" lang={row.lang || undefined}>
          {row.parts.map((p, i) =>
            p.type === "eq" ? (
              <span key={i}>{p.text}</span>
            ) : p.type === "del" ? (
              <del key={i} className="diff__del">
                <span className="visually-hidden">{t("item.cues.srDeleted")}</span>
                {p.text}
              </del>
            ) : (
              <ins key={i} className="diff__ins">
                <span className="visually-hidden">{t("item.cues.srInserted")}</span>
                {p.text}
              </ins>
            ),
          )}
          {row.pending && <small className="cue__tag">{t("item.cues.notCorrected")}</small>}
        </p>
      );
      break;
    case "parallel":
      body = (
        <div className="cue__text">
          <p lang={row.lang || undefined}>{row.original}</p>
          <p className="cue__translated" lang={row.targetLang || undefined}>
            {row.translated}
          </p>
        </div>
      );
      break;
  }
  return (
    <div
      className="cue"
      data-current={current || undefined}
      data-changed={row.kind === "diff" && row.changed ? true : undefined}
    >
      <button
        type="button"
        className="cue__time num"
        aria-current={current ? "true" : undefined}
        aria-label={t(current ? "item.cues.seekCurrent" : "item.cues.seekTo", { time })}
        onClick={() => onSeek(row.start)}
        {...focusProps}
      >
        {time}
      </button>
      <TimingMark
        timing={
          row.kind === "single" ? row.cue.timing : row.kind === "diff" ? row.timing : undefined
        }
      />
      {body}
    </div>
  );
}
