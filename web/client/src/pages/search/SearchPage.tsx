// 字幕全文搜尋（pages.md §3「字幕全文搜尋」、SRCH-R3～R10）：從媒體庫的搜尋欄進來（lazy route）。
//
//   - 查詢與語言在 URL（?q=&lang=），可以加書籤；游標分頁「載入更多」，游標失效時從第一頁重新讀取。
//   - 結果以字幕句為單位、依影音分組；命中範圍依 highlights（UTF-16 位移）切成 <mark>，不插入任何 HTML。
//   - 點結果打開影音詳情，播放器定位到那一句（?t=）。
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import { useEffect, useId, useMemo, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, EmptyState, ErrorCallout, LoadMore, Spinner } from "../../components";
import { formatNumber, formatTimecode } from "../../i18n";
import { usePageTitle } from "../../shell/usePageTitle";
import "../../styles/ui5.css";
import {
  groupByItem,
  jumpSecond,
  parseClock,
  queryProblem,
  splitHighlights,
  validateSearchParams,
  type SearchParams,
} from "./model";

type Result = components["schemas"]["SearchResult"];

const route = getRouteApi("/app/search");
/** 單頁筆數（API 1～100；OpenAPI 把 limit 定成字串） */
const PAGE_SIZE = "50";

export function SearchPage() {
  const { t } = useTranslation();
  usePageTitle(t("pages.search.title"));
  const raw = route.useSearch();
  const params = useMemo(() => validateSearchParams(raw as Record<string, unknown>), [raw]);
  const navigate = route.useNavigate();
  const problem = params.q ? queryProblem(params.q) : "empty";

  return (
    <div className="search-page">
      <div className="page__head">
        <h1 className="page__title">{t("pages.search.title")}</h1>
      </div>
      <SearchForm
        key={`${params.q ?? ""}\n${params.lang ?? ""}`}
        initial={params}
        onSubmit={(next) => void navigate({ search: next })}
      />
      {!params.q ? (
        <p className="secondary">{t("search.intro")}</p>
      ) : problem ? null : (
        <Results q={params.q} lang={params.lang} />
      )}
    </div>
  );
}

function SearchForm({
  initial,
  onSubmit,
}: {
  initial: SearchParams;
  onSubmit: (p: SearchParams) => void;
}) {
  const { t } = useTranslation();
  const id = useId();
  const [q, setQ] = useState(initial.q ?? "");
  const [lang, setLang] = useState(initial.lang ?? "");
  // 從 URL 帶來的查詢不合法時直接顯示原因；使用者輸入時等送出才檢查
  const [problem, setProblem] = useState(initial.q ? queryProblem(initial.q) : undefined);

  function submit(e: FormEvent) {
    e.preventDefault();
    const p = queryProblem(q);
    setProblem(p);
    if (p) return;
    const l = lang.trim();
    onSubmit({ q: q.trim(), ...(l ? { lang: l } : {}) });
  }

  return (
    <form className="search-form" role="search" onSubmit={submit} noValidate>
      <div className="field search-form__q">
        <label className="field__label" htmlFor={`${id}-q`}>
          {t("search.label")}
        </label>
        <input
          id={`${id}-q`}
          className="field__input"
          type="search"
          value={q}
          autoFocus={!initial.q}
          aria-invalid={problem ? true : undefined}
          aria-describedby={`${id}-q-hint`}
          onChange={(e) => setQ(e.target.value)}
        />
        <span id={`${id}-q-hint`} className={problem ? "field__error" : "field__hint"}>
          {problem ? t(`search.problem.${problem}`) : t("search.hint")}
        </span>
      </div>
      <div className="field search-form__lang">
        <label className="field__label" htmlFor={`${id}-lang`}>
          {t("search.lang")}
        </label>
        <input
          id={`${id}-lang`}
          className="field__input"
          value={lang}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => setLang(e.target.value)}
        />
      </div>
      <Button type="submit" variant="primary">
        {t("search.submit")}
      </Button>
    </form>
  );
}

function Results({ q, lang }: { q: string; lang?: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const key = queryKeys.search(q, lang ?? "");
  const list = useInfiniteQuery({
    queryKey: key,
    queryFn: ({ pageParam, signal }) =>
      api.get("/api/search", {
        query: { q, lang, limit: PAGE_SIZE, cursor: pageParam },
        signal,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
  });
  const rows = useMemo<Result[]>(
    () => list.data?.pages.flatMap((p) => p.results ?? []) ?? [],
    [list.data],
  );
  const total = list.data?.pages[0]?.total ?? 0;

  // 游標失效（server 重新啟動後 422）：從第一頁重新讀取
  const cursorExpired =
    list.isFetchNextPageError && isApiError(list.error) && list.error.status === 422;
  useEffect(() => {
    if (cursorExpired) void qc.resetQueries({ queryKey: key, exact: true });
  }, [cursorExpired, key, qc]);

  if (list.isPending) return <Spinner label={t("search.loading")} showLabel />;
  if (list.isError && !list.data)
    return (
      <ErrorCallout
        error={list.error}
        actions={
          isApiError(list.error) && list.error.status === 422
            ? []
            : [{ label: t("common.retry"), onClick: () => void list.refetch() }]
        }
      />
    );
  if (rows.length === 0) return <EmptyState message={t("search.none", { q })} />;
  return (
    <>
      <p className="secondary num" aria-live="polite">
        {t("search.total", { count: total, formatted: formatNumber(total) })}
      </p>
      <div className="search-results">
        {groupByItem(rows).map((group) => (
          <section
            key={`${group[0]!.item.id}-${group[0]!.lang}-${group[0]!.cue}`}
            className="search-group"
          >
            <h2 className="search-group__title">
              <Link to="/items/$id" params={{ id: group[0]!.item.id }}>
                {group[0]!.item.title || group[0]!.item.ref}
              </Link>
            </h2>
            <ul className="search-group__list">
              {group.map((r) => (
                <li key={`${r.lang}-${r.variant}-${r.cue}`}>
                  <Hit r={r} />
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
      <LoadMore
        loaded={rows.length}
        total={total}
        hasMore={!!list.hasNextPage}
        loading={list.isFetchingNextPage}
        error={list.isFetchNextPageError && !cursorExpired ? list.error : undefined}
        onLoadMore={() => void list.fetchNextPage()}
      />
    </>
  );
}

function Hit({ r }: { r: Result }) {
  const { t } = useTranslation();
  const at = jumpSecond(r.start, r.end);
  const time = formatTimecode(parseClock(r.start) ?? 0);
  return (
    <Link className="search-hit" to="/items/$id" params={{ id: r.item.id }} search={{ t: at }}>
      <span className="search-hit__time num">{time}</span>
      <span className="search-hit__body" lang={r.lang}>
        {r.before && <span className="search-hit__context">{r.before} </span>}
        <span className="search-hit__text">
          {splitHighlights(r.text, r.highlights).map((p, i) =>
            p.hit ? <mark key={i}>{p.text}</mark> : <span key={i}>{p.text}</span>,
          )}
        </span>
        {r.after && <span className="search-hit__context"> {r.after}</span>}
      </span>
      <span className="search-hit__meta secondary">
        {r.lang} · {t(`item.cues.modes.${r.variant}`, { defaultValue: r.variant })}
      </span>
    </Link>
  );
}
