// 「新增」抽屜一次貼上多個網址（pages.md §2 的「一行一個」、toast「已開始處理 2 部」）。
//
//   - 一行一個網址，空行與重複的行略過；最多 MAX_LINES 行，超過時不能送出。
//   - 每一行各自辨識（POST /api/resolve，和單一網址共用 query key）。不能新增的行就地標示、不擋其他行：
//     不是 http／https 網址、辨識失敗、頻道（要建立訂閱）、Podcast feed（要選集數，請一次貼一個）。
//     Bilibili 沒有指定分P 時新增全部分P，播放清單整份新增（和單一網址的預設相同）。
//   - 預估花費（POST /api/estimate）一行一行依序送：估算可能呼叫 yt-dlp，不同時送出多個；失敗只寫在那一行。
//   - 送出由抽屜依序逐行 POST /api/items（前端迴圈），結果合計在 toast。
import { useQueries, useQueryClient } from "@tanstack/react-query";
import { useId } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../api/client";
import { describeError } from "../api/describeError";
import type { components } from "../api/schema";
import { Spinner } from "../components";
import { formatUsd } from "../i18n";
import { isHttpUrl } from "./url";

type Resolution = components["schemas"]["Resolution"];
type EstimateBody = components["schemas"]["EstimateInputBody"];
type EstimateResult = components["schemas"]["EstimateResult"];

/** 一次最多幾行（每行各自辨識與預估） */
export const MAX_LINES = 20;

/** 去掉空白、空行與重複的行，保留順序 */
export function uniqueLines(text: string): string[] {
  return [
    ...new Set(
      text
        .split(/\r?\n/)
        .map((l) => l.trim())
        .filter(Boolean),
    ),
  ];
}

type OkLine = Extract<LineState, { kind: "ok" }>;
const isOk = (s: LineState): s is OkLine => s.kind === "ok";

export type LineState =
  | { url: string; kind: "invalid" }
  | { url: string; kind: "resolving" }
  | { url: string; kind: "error"; error: unknown }
  | { url: string; kind: "channel"; res: Resolution }
  | { url: string; kind: "feed"; res: Resolution }
  | {
      url: string;
      kind: "ok";
      res: Resolution;
      body: EstimateBody;
      estimate?: EstimateResult;
      estimating: boolean;
      estimateError?: unknown;
    };

/**
 * 每一行的辨識與預估。lines 已經是 debounce 過、去重的網址（最多 MAX_LINES 行）。
 * base 是每一行共用的內容（處理方案、單次覆寫）。
 */
export function useMultiLines({
  lines,
  enabled,
  base,
  estimateEnabled,
}: {
  lines: readonly string[];
  enabled: boolean;
  base: Omit<EstimateBody, "url">;
  estimateEnabled: boolean;
}): LineState[] {
  const qc = useQueryClient();
  const resolves = useQueries({
    queries: lines.map((url) => ({
      queryKey: ["resolve", url],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        api.post("/api/resolve", { body: { url }, signal }),
      enabled: enabled && isHttpUrl(url),
      retry: false,
      staleTime: 60_000,
    })),
  });

  const states: LineState[] = lines.map((url, i) => {
    if (!isHttpUrl(url)) return { url, kind: "invalid" };
    const r = resolves[i]!;
    if (r.isError) return { url, kind: "error", error: r.error };
    if (!r.data) return { url, kind: "resolving" };
    const res = r.data;
    if (res.subscribable) return { url, kind: "channel", res };
    if (res.kind === "feed") return { url, kind: "feed", res };
    return {
      url,
      kind: "ok",
      res,
      body: { url: res.url || url, ...base },
      estimating: false,
    };
  });

  // 預估一次一行：前面的行都估完（成功或失敗）才送下一行
  const ok = states.filter(isOk);
  const keys = ok.map((s) => ["estimate", JSON.stringify(s.body)]);
  // 第 i 行可以送出＝前面的行都已經有結果（成功或失敗）
  const settled = keys.map((key) => {
    const st = qc.getQueryState(key)?.status;
    return st === "success" || st === "error";
  });
  const firstUnsettled = settled.indexOf(false);
  const estimateQueries = ok.map((s, i) => ({
    queryKey: keys[i]!,
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      api.post("/api/estimate", { body: s.body, signal }),
    enabled: estimateEnabled && (firstUnsettled === -1 || i <= firstUnsettled),
    retry: false,
    staleTime: 60_000,
  }));
  const estimates = useQueries({ queries: estimateQueries });
  ok.forEach((s, i) => {
    const e = estimates[i]!;
    s.estimate = e.data;
    s.estimating = e.isFetching;
    s.estimateError = e.error ?? undefined;
  });
  return states;
}

/** 每一行的辨識結果與預估；errors 是上次送出失敗的行（網址 → 錯誤） */
export function MultiLines({
  states,
  errors,
  extra,
}: {
  states: readonly LineState[];
  errors: ReadonlyMap<string, unknown>;
  /** 超過 MAX_LINES 的行數 */
  extra: number;
}) {
  const { t } = useTranslation();
  const id = useId();
  const ok = states.filter(isOk);
  const items = ok.flatMap((s) => s.estimate?.items ?? []);
  const knownItems = items.filter((i) => i.total_usd != null).length;
  const total = ok.reduce((sum, s) => sum + (s.estimate?.total_usd ?? 0), 0);
  const anyUnknown = ok.some((s) => s.estimateError != null) || knownItems < items.length;
  const settled = ok.every((s) => !s.estimating && (s.estimate || s.estimateError != null));
  return (
    <section className="add__lines" aria-labelledby={`${id}-head`}>
      <h3 id={`${id}-head`} className="add__kind">
        {t("add.lines.title", { count: states.length + extra })}
      </h3>
      <ul className="add__line-list">
        {states.map((s) => (
          <li key={s.url} className="add__line" data-kind={s.kind}>
            <span className="add__line-url num">{s.url}</span>
            <LineStatus state={s} />
            {errors.has(s.url) && (
              <span className="add__line-error">{describeError(t, errors.get(s.url))}</span>
            )}
          </li>
        ))}
      </ul>
      {extra > 0 && (
        <p className="add__line-error">{t("add.lines.tooMany", { max: MAX_LINES, extra })}</p>
      )}
      {ok.length > 0 && settled && (
        <p className="add__estimate" role="status">
          {/* 只顯示已知金額；全部未知時不顯示 $0.00（同單一網址） */}
          {knownItems === 0
            ? t("add.estimateUnknown")
            : anyUnknown
              ? t("add.estimatePartial", { usd: formatUsd(total) })
              : t("add.estimate", { usd: formatUsd(total) })}
        </p>
      )}
    </section>
  );
}

function LineStatus({ state: s }: { state: LineState }) {
  const { t } = useTranslation();
  switch (s.kind) {
    case "invalid":
      return <span className="add__line-error">{t("add.invalidUrl")}</span>;
    case "resolving":
      return <Spinner label={t("add.resolving")} />;
    case "error":
      return <span className="add__line-error">{describeError(t, s.error)}</span>;
    case "channel":
      return <span className="add__line-error">{t("add.lines.channel")}</span>;
    case "feed":
      return <span className="add__line-error">{t("add.lines.feed")}</span>;
  }
  const platform = t(`platform.${s.res.platform}`, { defaultValue: s.res.platform });
  const kind = t(`add.kind.${s.res.kind}`, { platform, defaultValue: s.res.kind });
  const items = s.estimate?.items ?? [];
  const usd =
    s.estimate && items.length > 0 && items.every((i) => i.total_usd != null)
      ? s.estimate.total_usd
      : undefined;
  return (
    <span className="add__line-ok secondary">
      {kind}
      {s.res.title ? `${t("common.listSeparator")}${s.res.title}` : ""}
      {s.res.platform === "bilibili" && s.res.kind === "video" && !s.res.part
        ? `${t("common.listSeparator")}${t("add.lines.allParts")}`
        : ""}
      {s.res.requires_cookie ? `${t("common.listSeparator")}${t("add.lines.cookie")}` : ""}
      {s.estimating
        ? `${t("common.listSeparator")}${t("add.estimating")}`
        : s.estimateError
          ? `${t("common.listSeparator")}${t("add.lines.noEstimate")}`
          : usd !== undefined
            ? `${t("common.listSeparator")}${formatUsd(usd)}`
            : s.estimate
              ? `${t("common.listSeparator")}${t("add.lines.noEstimate")}`
              : ""}
    </span>
  );
}
