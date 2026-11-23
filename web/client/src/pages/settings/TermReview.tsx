// 術語審核（GLOS-R4、R5、R7）：跨頻道的候選清單，依頻道分組；接受、改寫後接受、拒絕，
// 選擇加到這個頻道或全域；可以多選後一次決定。由 GlossarySection lazy 載入，不進初始 bundle。
//
// 單筆與批次都送 POST /api/term-candidates/decide（整批一個交易，G4）：
//   - 422 的 errors[].path 是 decisions.<n>[.<欄位>]，n 是送出的順序，對回那一列顯示。
//   - 409 glossary_full：詞彙表已滿，顯示伺服器的訊息；409 term_candidate_decided：已在別處決定，重新讀取。
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useMemo, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, ErrorCallout, LoadMore, SelectField, TextField, useToast } from "../../components";
import { formatDateTime } from "../../i18n";
import "../../styles/ui5.css";
import { Loading } from "./shared";

type Review = components["schemas"]["TermCandidateReview"];
type Decision = components["schemas"]["TermDecisionBody"];
const SCOPES = ["channel", "global"] as const;
type Scope = (typeof SCOPES)[number];

const STATES = ["pending", "accepted", "rejected", "all"] as const;
type StateFilter = (typeof STATES)[number];
const PAGE_SIZE = 50;
/** API 的上限：一次最多 100 項 */
const MAX_BATCH = 100;

interface Filter {
  state: StateFilter;
  channel: string;
}

export function TermReview() {
  const { t } = useTranslation();
  const [filter, setFilter] = useState<Filter>({ state: "pending", channel: "" });
  const [draft, setDraft] = useState<Filter>(filter);

  function apply(e: FormEvent) {
    e.preventDefault();
    setFilter({ state: draft.state, channel: draft.channel.trim() });
  }

  return (
    <>
      <form className="settings-inline-form" onSubmit={apply} noValidate>
        <SelectField
          label={t("settings.glossary.review.stateFilter")}
          value={draft.state}
          options={STATES.map((s) => ({
            value: s,
            label:
              s === "all" ? t("settings.glossary.review.all") : t(`settings.glossary.state.${s}`),
          }))}
          onChange={(e) => setDraft({ ...draft, state: e.target.value as StateFilter })}
        />
        <TextField
          label={t("settings.glossary.review.channelFilter")}
          hint={t("settings.glossary.channelFormat")}
          autoComplete="off"
          spellCheck={false}
          value={draft.channel}
          onChange={(e) => setDraft({ ...draft, channel: e.target.value })}
        />
        <div className="settings-actions">
          <Button type="submit">{t("settings.glossary.review.apply")}</Button>
        </div>
      </form>
      <ReviewList key={`${filter.state}\n${filter.channel}`} filter={filter} />
    </>
  );
}

interface RowState {
  scope: Scope;
  /** 改寫後接受：輸入框的內容；沒有打開時是 undefined */
  rewrite?: string;
}

function ReviewList({ filter }: { filter: Filter }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const key = queryKeys.termReview(filter.state, filter.channel);
  const list = useInfiniteQuery({
    queryKey: key,
    queryFn: ({ pageParam, signal }) =>
      api.get("/api/term-candidates", {
        query: {
          state: filter.state,
          channel: filter.channel || undefined,
          limit: PAGE_SIZE,
          cursor: pageParam,
        },
        signal,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
  });
  const rows = useMemo<Review[]>(
    () => list.data?.pages.flatMap((p) => p.candidates ?? []) ?? [],
    [list.data],
  );
  const total = list.data?.pages[0]?.total ?? 0;

  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [rowState, setRowState] = useState<Record<string, RowState>>({});
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState<unknown>();
  const [stale, setStale] = useState(false);
  const [busy, setBusy] = useState(false);

  const stateOf = (id: string): RowState => rowState[id] ?? { scope: SCOPES[0] };
  const patchRow = (id: string, p: Partial<RowState>) =>
    setRowState((m) => ({ ...m, [id]: { ...stateOf(id), ...p } }));
  const pendingIds = rows.filter((r) => r.state === "pending").map((r) => r.id);
  const chosen = pendingIds.filter((id) => selected.has(id));

  async function decide(decisions: Decision[]) {
    setBusy(true);
    setError(undefined);
    setStale(false);
    setRowErrors({});
    try {
      const out = await api.post("/api/term-candidates/decide", { body: { decisions } });
      const done = out?.candidates ?? [];
      const accepted = done.filter((c) => c.state === "accepted").length;
      toast.show({
        title: t("settings.glossary.review.done", {
          accepted,
          rejected: done.length - accepted,
        }),
      });
      setSelected(new Set());
      setRowState((m) => {
        const next = { ...m };
        for (const d of decisions) delete next[d.candidate];
        return next;
      });
      // 詞彙表的內容可能改變（頻道或全域）
      if (accepted > 0) await qc.invalidateQueries({ queryKey: ["glossary"] });
    } catch (err) {
      if (isApiError(err) && err.code === "term_candidate_decided") setStale(true);
      else if (isApiError(err) && err.status === 422 && err.body.errors?.length) {
        // decisions.<n>[.<欄位>] → 第 n 個送出的項目
        const byRow: Record<string, string> = {};
        for (const f of err.body.errors) {
          const m = /^decisions\.(\d+)/.exec(f.path);
          const d = m ? decisions[Number(m[1])] : undefined;
          if (d) byRow[d.candidate] = f.message;
        }
        setRowErrors(byRow);
        if (Object.keys(byRow).length === 0) setError(err);
      } else setError(err);
    } finally {
      setBusy(false);
      await qc.invalidateQueries({ queryKey: queryKeys.termCandidatesAll });
    }
  }

  const accept = (r: Review, term?: string): Decision => ({
    candidate: r.id,
    state: "accepted",
    scope: stateOf(r.id).scope,
    ...(term !== undefined ? { term } : {}),
  });
  const reject = (id: string): Decision => ({ candidate: id, state: "rejected" });
  const byId = new Map(rows.map((r) => [r.id, r]));

  if (list.isPending) return <Loading />;
  if (list.isError && !list.data)
    return (
      <ErrorCallout
        error={list.error}
        actions={[{ label: t("common.retry"), onClick: () => void list.refetch() }]}
      />
    );

  // 依頻道分組（保留第一次出現的順序；清單本身依提出時間由舊到新）
  const groups = new Map<string, Review[]>();
  for (const r of rows) {
    const k = r.channel?.id ?? "";
    groups.set(k, [...(groups.get(k) ?? []), r]);
  }

  return (
    <div className="term-review">
      {stale && (
        <p className="settings-note" role="status">
          {t("settings.glossary.review.stale")}
        </p>
      )}
      {error !== undefined && <ErrorCallout error={error} />}
      {pendingIds.length > 0 && (
        <div
          className="term-review__bar"
          role="group"
          aria-label={t("settings.glossary.review.batch")}
        >
          <label className="check-row">
            <input
              type="checkbox"
              checked={chosen.length === pendingIds.length}
              onChange={(e) => setSelected(new Set(e.target.checked ? pendingIds : []))}
            />
            {t("settings.glossary.review.selectAll")}
          </label>
          <span className="secondary" aria-live="polite">
            {t("settings.glossary.review.selected", { count: chosen.length })}
          </span>
          <Button
            disabled={busy || chosen.length === 0 || chosen.length > MAX_BATCH}
            onClick={() => void decide(chosen.map((id) => accept(byId.get(id)!)))}
          >
            {t("settings.glossary.review.acceptSelected")}
          </Button>
          <Button
            variant="text"
            disabled={busy || chosen.length === 0 || chosen.length > MAX_BATCH}
            onClick={() => void decide(chosen.map(reject))}
          >
            {t("settings.glossary.review.rejectSelected")}
          </Button>
          {chosen.length > MAX_BATCH && (
            <span className="field__error">{t("settings.glossary.review.tooMany")}</span>
          )}
        </div>
      )}
      {rows.length === 0 ? (
        <p className="settings-panel__description">{t("settings.glossary.noCandidates")}</p>
      ) : (
        [...groups.values()].map((group) => {
          const ch = group[0]!.channel;
          return (
            <section key={ch?.id ?? ""} className="term-review__group">
              <h4 className="term-review__channel">
                {ch?.title || ch?.key || t("settings.glossary.review.noChannel")}
                {ch?.key && ch.title && <span className="secondary"> · {ch.key}</span>}
              </h4>
              <ul className="settings-rows">
                {group.map((r) => (
                  <Row
                    key={r.id}
                    r={r}
                    state={stateOf(r.id)}
                    error={rowErrors[r.id]}
                    busy={busy}
                    selected={selected.has(r.id)}
                    onSelect={(on) =>
                      setSelected((s) => {
                        const n = new Set(s);
                        if (on) n.add(r.id);
                        else n.delete(r.id);
                        return n;
                      })
                    }
                    onPatch={(p) => patchRow(r.id, p)}
                    onAccept={(term) => void decide([accept(r, term)])}
                    onReject={() => void decide([reject(r.id)])}
                  />
                ))}
              </ul>
            </section>
          );
        })
      )}
      {rows.length > 0 && (
        <LoadMore
          loaded={rows.length}
          total={total}
          hasMore={!!list.hasNextPage}
          loading={list.isFetchingNextPage}
          error={list.isFetchNextPageError ? list.error : undefined}
          onLoadMore={() => void list.fetchNextPage()}
        />
      )}
    </div>
  );
}

function Row({
  r,
  state,
  error,
  busy,
  selected,
  onSelect,
  onPatch,
  onAccept,
  onReject,
}: {
  r: Review;
  state: RowState;
  error?: string;
  busy: boolean;
  selected: boolean;
  onSelect: (on: boolean) => void;
  onPatch: (p: Partial<RowState>) => void;
  onAccept: (term?: string) => void;
  onReject: () => void;
}) {
  const { t } = useTranslation();
  const pending = r.state === "pending";
  const scopeLabel = (s: string) =>
    s === SCOPES[1]
      ? t("settings.glossary.review.scopeGlobal")
      : t("settings.glossary.review.scopeChannel");
  // GLOS-R5：影音已永久刪除時 item 是 null（schema 沒有標 nullable，見 UI README 待決問題）
  const item = r.item as Review["item"] | null;
  return (
    <li className="settings-row term-review__row" data-error={error ? true : undefined}>
      <div className="term-review__pick">
        {pending && (
          <input
            type="checkbox"
            checked={selected}
            aria-label={t("settings.glossary.review.select", { term: r.term })}
            onChange={(e) => onSelect(e.target.checked)}
          />
        )}
        <div className="settings-row__main">
          <span className="settings-row__name">{r.term}</span>
          <span className="settings-row__meta">
            {formatDateTime(r.created_at)}
            {" · "}
            {item?.id ? (
              <Link to="/items/$id" params={{ id: item.id }}>
                {item.title || item.ref}
              </Link>
            ) : (
              t("settings.glossary.review.noItem")
            )}
            {!pending && (
              <>
                {" · "}
                {r.state === "accepted"
                  ? t("settings.glossary.review.acceptedAs", {
                      term: r.decided_term ?? r.term,
                      scope: scopeLabel(r.decided_scope ?? SCOPES[0]),
                    })
                  : t("settings.glossary.state.rejected")}
              </>
            )}
          </span>
          {error && <span className="field__error">{error}</span>}
        </div>
      </div>
      {pending && state.rewrite === undefined && (
        <div className="settings-row__actions term-review__actions">
          <label className="term-review__scope">
            <span className="visually-hidden">
              {t("settings.glossary.review.scopeOf", { term: r.term })}
            </span>
            <select
              className="field__input"
              value={state.scope}
              onChange={(e) => onPatch({ scope: e.target.value as Scope })}
            >
              {SCOPES.map((s) => (
                <option key={s} value={s}>
                  {scopeLabel(s)}
                </option>
              ))}
            </select>
          </label>
          <Button
            disabled={busy}
            aria-label={t("settings.glossary.acceptNamed", { term: r.term })}
            onClick={() => onAccept()}
          >
            {t("settings.glossary.accept")}
          </Button>
          <Button
            variant="outline"
            disabled={busy}
            aria-label={t("settings.glossary.review.rewriteNamed", { term: r.term })}
            onClick={() => onPatch({ rewrite: r.term })}
          >
            {t("settings.glossary.review.rewrite")}
          </Button>
          <Button
            variant="text"
            disabled={busy}
            aria-label={t("settings.glossary.rejectNamed", { term: r.term })}
            onClick={onReject}
          >
            {t("settings.glossary.reject")}
          </Button>
        </div>
      )}
      {pending && state.rewrite !== undefined && (
        <form
          className="settings-inline-form term-review__rewrite"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            if (state.rewrite?.trim()) onAccept(state.rewrite.trim());
          }}
        >
          <TextField
            label={t("settings.glossary.review.rewriteLabel", { term: r.term })}
            autoFocus
            autoComplete="off"
            spellCheck={false}
            value={state.rewrite}
            onChange={(e) => onPatch({ rewrite: e.target.value })}
          />
          <SelectField
            label={t("settings.glossary.review.scope")}
            value={state.scope}
            options={SCOPES.map((s) => ({ value: s, label: scopeLabel(s) }))}
            onChange={(e) => onPatch({ scope: e.target.value as Scope })}
          />
          <div className="settings-actions">
            <Button type="submit" variant="primary" disabled={busy || !state.rewrite.trim()}>
              {t("settings.glossary.review.confirmRewrite")}
            </Button>
            <Button variant="text" onClick={() => onPatch({ rewrite: undefined })}>
              {t("settings.glossary.review.cancel")}
            </Button>
          </div>
        </form>
      )}
    </li>
  );
}
