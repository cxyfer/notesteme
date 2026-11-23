// 媒體庫（M1 基本版，docs/ui/pages.md §3、LIB-R1～R4、R9、R10）。
//
//   - 篩選條件全部在 URL（pages/library/filters.ts），重新整理或分享後結果相同（LIB-R2）。
//   - GET /api/items 的 keyset cursor 無限捲動；列表虛擬化，只渲染可視範圍（NFR-PERF-10）。
//   - 即時更新不打亂列表：item.created 只累計「有 N 個新項目」，點擊後才回到頂部重新載入；
//     步驟狀態只更新那一列（每列的 detail query，見 LibraryRow）。
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { useAddDrawer } from "../add/AddContext";
import { api } from "../api/client";
import { isApiError } from "../api/errors";
import { queryKeys } from "../api/queryKeys";
import {
  Button,
  EmptyState,
  ErrorCallout,
  LoadMore,
  Spinner,
  VirtualList,
  type VirtualListHandle,
} from "../components";
import { useServerEvent, useStreamReconnect } from "../events/useEventStream";
import { formatNumber } from "../i18n";
import { usePageTitle } from "../shell/usePageTitle";
import { isEditableTarget } from "../shell/keyboard";
import { FilterBar, SearchBox, type SearchPatch } from "./library/FilterBar";
import {
  hasFilters,
  toApiQuery,
  validateLibrarySearch,
  type LibrarySearch,
} from "./library/filters";
import { ContinueBar } from "./library/ContinueBar";
import { LibraryRow } from "./library/LibraryRow";
import type { ApiItem } from "./library/stageView";

const route = getRouteApi("/app/library");

/** 單頁筆數（API 上限 200） */
export const PAGE_SIZE = 100;

export function LibraryPage() {
  const { t } = useTranslation();
  usePageTitle(t("pages.library.title"));
  // match 的 search 也帶著上層 route 的原始參數（沒有驗證過）：這裡再驗證一次，只留認得的條件
  const rawSearch = route.useSearch();
  const search = useMemo(
    () => validateLibrarySearch(rawSearch as Record<string, unknown>),
    [rawSearch],
  );
  const navigate = route.useNavigate();
  const qc = useQueryClient();
  const addDrawer = useAddDrawer();

  const apiQuery = useMemo(() => toApiQuery(search), [search]);
  const listKey = useMemo(() => queryKeys.items.list(apiQuery), [apiQuery]);
  const list = useInfiniteQuery({
    queryKey: listKey,
    queryFn: ({ pageParam, signal }) =>
      api.get("/api/items", {
        query: { ...apiQuery, limit: PAGE_SIZE, cursor: pageParam },
        signal,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
    // 列表只在使用者要求時重新載入（換篩選、點「有 N 個新項目」），不因即時更新或重新聚焦而重排
    staleTime: Infinity,
  });

  const rows = useMemo<ApiItem[]>(
    () => list.data?.pages.flatMap((p) => p.items ?? []) ?? [],
    [list.data],
  );
  const total = list.data?.pages[0]?.total ?? 0;

  const setSearch = useCallback(
    (patch: SearchPatch, opts: { replace?: boolean } = {}) => {
      void navigate({
        search: (prev: LibrarySearch) => validateLibrarySearch({ ...prev, ...patch }),
        replace: opts.replace,
      });
    },
    [navigate],
  );
  const clearFilters = useCallback(() => void navigate({ search: {} }), [navigate]);

  // ---- 新項目與重新連上：只提示，使用者點擊後才重新載入 ----
  const listRef = useRef<VirtualListHandle>(null);
  const keyHash = JSON.stringify(listKey);
  // 換篩選後計數歸零：狀態記住是哪一組條件的計數
  const [fresh, setFresh] = useState({ key: keyHash, count: 0, stale: false });
  const notice = fresh.key === keyHash ? fresh : { key: keyHash, count: 0, stale: false };
  const bump = (patch: (n: typeof notice) => Partial<typeof notice>) =>
    setFresh((f) => {
      const base = f.key === keyHash ? f : { key: keyHash, count: 0, stale: false };
      return { ...base, ...patch(base) };
    });
  useServerEvent(() => bump((n) => ({ count: n.count + 1 })), ["item.created"]);
  useStreamReconnect(() => bump(() => ({ stale: true })));
  const reload = useCallback(() => {
    setFresh({ key: keyHash, count: 0, stale: false });
    listRef.current?.scrollToTop();
    void qc.resetQueries({ queryKey: listKey, exact: true });
  }, [keyHash, listKey, qc]);

  // 游標失效（server 重新啟動後回 422）：從第一頁重新讀取
  const cursorExpired =
    list.isFetchNextPageError && isApiError(list.error) && list.error.status === 422;
  useEffect(() => {
    if (cursorExpired) void qc.resetQueries({ queryKey: listKey, exact: true });
  }, [cursorExpired, listKey, qc]);

  // ---- 宣告（aria-live）：只宣告完成、失敗、需要你處理（docs/ui/README.md §6） ----
  const [announcement, setAnnouncement] = useState("");
  useServerEvent(
    (ev) => {
      const title = ev.title || ev.item;
      const stage = t(`stage.${ev.stage}`, { defaultValue: ev.stage ?? "" });
      if (ev.status === "failed") setAnnouncement(t("library.live.failed", { title, stage }));
      else if (ev.status === "blocked")
        setAnnouncement(t("library.live.blocked", { title, stage }));
      else if (ev.status === "done" && ev.stage === "publish")
        setAnnouncement(t("library.live.done", { title }));
    },
    ["stage.status"],
  );

  // ---- 鍵盤：「/」聚焦搜尋框 ----
  const searchRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key !== "/" || e.ctrlKey || e.metaKey || e.altKey || isEditableTarget(e.target)) return;
      e.preventDefault();
      searchRef.current?.focus();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  const { hasNextPage, isFetchingNextPage, fetchNextPage } = list;
  const loadMore = useCallback(() => {
    if (hasNextPage && !isFetchingNextPage) void fetchNextPage();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);
  const onEndReached = useCallback(() => {
    // 出錯後不自動重試，等使用者按「重試」
    if (!list.isFetchNextPageError) loadMore();
  }, [list.isFetchNextPageError, loadMore]);

  const filtered = hasFilters(search);

  return (
    <div className="library">
      <div className="page__head">
        <h1 className="page__title">{t("pages.library.title")}</h1>
        {list.isSuccess && (
          <p className="library__total secondary num" aria-live="polite">
            {t("library.total", { count: total, formatted: formatNumber(total) })}
          </p>
        )}
      </div>

      <SearchBox
        ref={searchRef}
        value={search.q}
        onCommit={(q) => setSearch({ q }, { replace: true })}
      />
      {/* 字幕全文搜尋（SRCH）：搜尋欄的另一個選擇，結果在 /search */}
      <p className="library__fulltext">
        <Link to="/search" search={search.q ? { q: search.q } : {}}>
          {search.q ? t("search.fromLibraryQ", { q: search.q }) : t("search.fromLibrary")}
        </Link>
      </p>
      <FilterBar search={search} onChange={setSearch} onClear={clearFilters} />

      {search.blocked === "awaiting_user" && rows.length > 0 && (
        <ContinueBar ids={rows.map((r) => r.id)} onDone={reload} />
      )}

      <p className="visually-hidden" aria-live="polite">
        {announcement}
      </p>
      {(notice.count > 0 || notice.stale) && (
        <div className="library__notice" aria-live="polite">
          <Button onClick={reload}>
            {notice.count > 0
              ? t("events.newItems", { count: notice.count })
              : t("library.staleList")}
          </Button>
        </div>
      )}

      {list.isPending ? (
        <Spinner label={t("library.loading")} showLabel />
      ) : list.isError && !list.data ? (
        <ErrorCallout
          error={list.error}
          actions={[{ label: t("common.retry"), onClick: () => void list.refetch() }]}
        />
      ) : rows.length === 0 ? (
        filtered ? (
          <EmptyState
            message={t("library.emptyFiltered")}
            action={{ label: t("library.filters.clearFilters"), onClick: clearFilters }}
          />
        ) : (
          <EmptyState
            message={t("library.empty")}
            action={{ label: t("add.open"), onClick: () => addDrawer.open() }}
          />
        )
      ) : (
        <VirtualList
          ref={listRef}
          className="library__list"
          label={t("library.listLabel")}
          items={rows}
          total={total}
          getKey={(it) => it.id}
          estimateSize={88}
          onEndReached={onEndReached}
          renderItem={(item, { focusProps }) => (
            <LibraryRow
              item={item}
              focusProps={focusProps}
              onFilterChannel={(channel) => setSearch({ channel })}
            />
          )}
          footer={
            <LoadMore
              loaded={rows.length}
              total={total}
              hasMore={!!list.hasNextPage}
              loading={list.isFetchingNextPage}
              error={list.isFetchNextPageError && !cursorExpired ? list.error : undefined}
              onLoadMore={() => void list.fetchNextPage()}
            />
          }
        />
      )}
    </div>
  );
}
