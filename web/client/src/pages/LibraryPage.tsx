// 媒體庫（M1 基本版，docs/ui/pages.md §3、LIB-R1～R4、R9、R10）。
//
//   - 篩選條件全部在 URL（pages/library/filters.ts），重新整理或分享後結果相同（LIB-R2）。
//   - GET /api/items 的 keyset cursor 無限捲動；列表虛擬化，只渲染可視範圍（NFR-PERF-10）。
//   - 即時更新不打亂列表：item.created 只累計「有 N 個新項目」，點擊後才回到頂部重新載入；
//     步驟狀態只更新那一列（每列的 detail query，見 LibraryRow）。
//   - 分組（LIB-R4）：預設依頻道，可以改成依發布日、狀態或不分組（網址的 group）；API 的 group_by 讓同一組連續，
//     列表在每組第一列的上方顯示組名（library/groups.ts）。「格」檢視只照分組的順序，不顯示組名。
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
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
  AWAITING_USER,
  hasFilters,
  toApiQuery,
  validateLibrarySearch,
  type LibrarySearch,
} from "./library/filters";
import {
  DEFAULT_GROUPING,
  groupByParam,
  groupCounts,
  groupKey,
  groupLabel,
  GROUPINGS,
  type Grouping,
  type GroupCount,
} from "./library/groups";
import { LibraryRow } from "./library/LibraryRow";
import type { ApiItem } from "./library/stageView";
import type { BulkBarProps } from "./library/BulkBar";

// 批次操作列只有選取時才需要：lazy 載入，不進初始 bundle（UI-7）
const BulkBar = lazy(() => import("./library/BulkBar").then((m) => ({ default: m.BulkBar })));
// 「格」檢視（UI-8）只在切換時載入
const LibraryGrid = lazy(() =>
  import("./library/LibraryGrid").then((m) => ({ default: m.LibraryGrid })),
);

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
  // 分組不是篩選條件：不放進 apiQuery（批次操作用它選「全部符合的」），只送列表
  const groupBy = groupByParam(search.group);
  const listKey = useMemo(
    () => queryKeys.items.list({ ...apiQuery, group_by: groupBy }),
    [apiQuery, groupBy],
  );
  const list = useInfiniteQuery({
    queryKey: listKey,
    queryFn: ({ pageParam, signal }) =>
      api.get("/api/items", {
        query: { ...apiQuery, group_by: groupBy, limit: PAGE_SIZE, cursor: pageParam },
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
  const counts = useMemo(
    () => groupCounts(rows, search.group, !!list.hasNextPage),
    [rows, search.group, list.hasNextPage],
  );

  const setSearch = useCallback(
    (patch: SearchPatch, opts: { replace?: boolean } = {}) => {
      void navigate({
        search: (prev: LibrarySearch) => validateLibrarySearch({ ...prev, ...patch }),
        replace: opts.replace,
      });
    },
    [navigate],
  );
  // 清除篩選時保留顯示方式與分組（都不是篩選條件）
  const grid = search.view === "grid";
  const group = search.group;
  const clearFilters = useCallback(
    () =>
      void navigate({
        search: { ...(grid ? { view: "grid" as const } : {}), ...(group ? { group } : {}) },
      }),
    [navigate, grid, group],
  );

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

  // ---- 批次操作的選取（UI-7）：換篩選時清空，避免畫面外、條件外的影音被一起處理 ----
  const [picked, setPicked] = useState({ key: keyHash, ids: new Set<string>(), last: -1 });
  const sel = picked.key === keyHash ? picked : { key: keyHash, ids: new Set<string>(), last: -1 };
  const clearSelection = useCallback(
    () => setPicked({ key: keyHash, ids: new Set(), last: -1 }),
    [keyHash],
  );
  const toggle = (index: number, on: boolean, shift: boolean) => {
    const ids = new Set(sel.ids);
    // Shift 點擊：從上一次點的列到這一列都設成同一個狀態
    const from = shift && sel.last >= 0 ? Math.min(sel.last, index) : index;
    const to = shift && sel.last >= 0 ? Math.max(sel.last, index) : index;
    for (let i = from; i <= to; i++) {
      const id = rows[i]?.id;
      if (id) {
        if (on) ids.add(id);
        else ids.delete(id);
      }
    }
    setPicked({ key: keyHash, ids, last: index });
  };
  const allLoaded = rows.length > 0 && rows.every((r) => sel.ids.has(r.id));
  const onBulkDone: BulkBarProps["onDone"] = (action, outcome) => {
    for (const x of outcome.items) {
      if (!x.id) continue;
      if (action === "delete" && x.ok) qc.removeQueries({ queryKey: queryKeys.items.detail(x.id) });
      else void qc.invalidateQueries({ queryKey: queryKeys.items.detail(x.id) });
    }
    // 已讀／未讀不改變列表的組成（除非用已讀狀態篩選），只更新那幾列
    if (action === "mark_read" || action === "mark_unread") {
      if (search.read_state) reload();
      return;
    }
    reload();
  };

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
        {/* UI-7：垃圾桶（pages.md §3） */}
        <Link to="/trash" className="library__trash">
          {t("trash.title")}
        </Link>
      </p>
      <FilterBar search={search} onChange={setSearch} onClear={clearFilters} />

      {/* 等你決定（DIG-R7）：勾選要繼續的影音，由批次操作列「繼續處理」（UI-8 起不再一次送已載入的全部） */}
      {search.blocked === AWAITING_USER && !grid && rows.length > 0 && sel.ids.size === 0 && (
        <p className="library__awaiting secondary">{t("library.awaiting.hint")}</p>
      )}
      {rows.length > 0 && (
        <div className="library__select">
          {!grid && (
            <label className="check">
              <input
                type="checkbox"
                checked={allLoaded}
                onChange={(e) =>
                  setPicked({
                    key: keyHash,
                    ids: e.target.checked ? new Set(rows.map((r) => r.id)) : new Set(),
                    last: -1,
                  })
                }
              />
              <span>{t("library.bulk.pickLoaded", { count: rows.length })}</span>
            </label>
          )}
          {/* LIB-R4 的分組 */}
          <label className="library__grouping">
            <span>{t("library.group.label")}</span>
            <select
              className="field__input"
              value={search.group ?? DEFAULT_GROUPING}
              onChange={(e) => {
                const g = e.target.value as Grouping;
                setSearch({ group: g === DEFAULT_GROUPING ? undefined : g }, { replace: true });
              }}
            >
              {GROUPINGS.map((g) => (
                <option key={g} value={g}>
                  {t(`library.group.by.${g}`)}
                </option>
              ))}
            </select>
          </label>
          {/* pages.md §3 的「列｜格」 */}
          <div className="library__view" role="group" aria-label={t("library.view.label")}>
            <Button
              aria-pressed={!grid}
              onClick={() => setSearch({ view: undefined }, { replace: true })}
            >
              {t("library.view.list")}
            </Button>
            <Button
              aria-pressed={grid}
              onClick={() => setSearch({ view: "grid" }, { replace: true })}
            >
              {t("library.view.grid")}
            </Button>
          </div>
        </div>
      )}
      {!grid && sel.ids.size > 0 && (
        <Suspense fallback={<Spinner label={t("common.loading")} />}>
          <BulkBar
            ids={[...sel.ids]}
            total={total}
            apiQuery={apiQuery}
            awaiting={search.blocked === AWAITING_USER}
            onSelect={(ids) => setPicked({ key: keyHash, ids: new Set(ids), last: -1 })}
            onClear={clearSelection}
            onDone={onBulkDone}
          />
        </Suspense>
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
      ) : grid ? (
        <Suspense fallback={<Spinner label={t("library.loading")} showLabel />}>
          <LibraryGrid
            items={rows}
            more={{
              loaded: rows.length,
              total,
              hasMore: !!list.hasNextPage,
              loading: list.isFetchingNextPage,
              error: list.isFetchNextPageError && !cursorExpired ? list.error : undefined,
              onLoadMore: () => void list.fetchNextPage(),
            }}
          />
        </Suspense>
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
          renderItem={(item, { index, focusProps }) => (
            <>
              {groupBy &&
                (index === 0 || groupKey(rows[index - 1]!, group) !== groupKey(item, group)) && (
                  <h2 className="library__group section__title">
                    {groupLabel(t, item, group)} <GroupCountText count={counts.get(index)} />
                  </h2>
                )}
              <LibraryRow
                item={item}
                focusProps={focusProps}
                onFilterChannel={(channel) => setSearch({ channel })}
                selected={sel.ids.has(item.id)}
                onSelect={(on, shift) => toggle(index, on, shift)}
              />
            </>
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

/** 組名旁的「3 部」；這組可能延續到還沒載入的頁時寫「至少 3 部」 */
function GroupCountText({ count }: { count: GroupCount | undefined }) {
  const { t } = useTranslation();
  if (!count) return null;
  const formatted = formatNumber(count.count);
  return (
    <span className="library__group-count secondary num">
      {count.complete
        ? t("library.group.count", { count: count.count, formatted })
        : t("library.group.countAtLeast", { count: count.count, formatted })}
    </span>
  );
}
