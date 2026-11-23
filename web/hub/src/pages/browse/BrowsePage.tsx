// 首頁：依平台、頻道、影片、語言、種類、變體與三個模型篩選分享的版本（HREP-R16，HUB-03）。
// 篩選在 URL；送出表單才查詢。結果以 cursor 分頁（「載入更多」）。Hub 不做字幕全文搜尋（HREP 不在範圍內）。
import { useInfiniteQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import {
  Button,
  EmptyState,
  ErrorCallout,
  SelectField,
  Spinner,
  TextField,
} from "../../components";
import { usePageTitle } from "../../shell/usePageTitle";
import { VersionSummary } from "../shared/VersionSummary";
import { videoLabel, videoLink } from "../versionText";
import {
  hasFilters,
  KINDS,
  PLATFORMS,
  TEXT_FILTERS,
  validateBrowseSearch,
  VARIANTS,
  type BrowseSearch,
} from "./filters";

export function BrowsePage() {
  const { t } = useTranslation();
  usePageTitle(t("browse.title"));
  const search = useSearch({ from: "/" });
  const navigate = useNavigate();
  const [draft, setDraft] = useState<BrowseSearch>(search);
  const [syncedFrom, setSyncedFrom] = useState(search);
  if (syncedFrom !== search) {
    // URL 改變（上一頁、清除篩選）時同步表單
    setSyncedFrom(search);
    setDraft(search);
  }

  const q = useInfiniteQuery({
    queryKey: queryKeys.versions({ ...search }),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam, signal }) =>
      api.get("/api/v1/versions", {
        query: { ...search, limit: 50, cursor: pageParam },
        signal,
      }),
    getNextPageParam: (last) => last.next_cursor || undefined,
  });
  const versions = q.data?.pages.flatMap((p) => p.versions ?? []) ?? [];
  const errs = fieldErrorsOf(q.error);
  const fieldErr = (k: string) => errs[`query.${k}`];

  const submit = (e: FormEvent) => {
    e.preventDefault();
    void navigate({ to: "/", search: validateBrowseSearch({ ...draft }) });
  };
  const set = (k: keyof BrowseSearch, v: string) =>
    setDraft((d) => ({ ...d, [k]: v || undefined }));
  const options = (values: readonly string[], prefix: string) => [
    { value: "", label: t("browse.any") },
    ...values.map((v) => ({ value: v, label: t(`${prefix}.${v}`, { defaultValue: v }) })),
  ];

  return (
    <section aria-labelledby="browse-title">
      <h1 id="browse-title" className="page__title">
        {t("browse.title")}
      </h1>
      <p className="secondary">{t("browse.intro")}</p>
      <form className="filters" onSubmit={submit} role="search" aria-label={t("browse.filters")}>
        <SelectField
          label={t("browse.platform")}
          value={draft.platform ?? ""}
          options={options(PLATFORMS, "platform")}
          onChange={(e) => set("platform", e.target.value)}
          error={fieldErr("platform")}
        />
        <SelectField
          label={t("browse.kind")}
          value={draft.kind ?? ""}
          options={options(KINDS, "kind")}
          onChange={(e) => set("kind", e.target.value)}
          error={fieldErr("kind")}
        />
        <SelectField
          label={t("browse.variant")}
          value={draft.variant ?? ""}
          options={options(VARIANTS, "variant")}
          onChange={(e) => set("variant", e.target.value)}
          error={fieldErr("variant")}
        />
        {TEXT_FILTERS.map((k) => (
          <TextField
            key={k}
            label={t(`browse.${k}`)}
            hint={
              k === "video"
                ? t("browse.videoHint")
                : k === "lang"
                  ? t("browse.langHint")
                  : undefined
            }
            value={draft[k] ?? ""}
            maxLength={256}
            onChange={(e) => set(k, e.target.value)}
            error={fieldErr(k)}
          />
        ))}
        <SelectField
          label={t("browse.sort")}
          value={draft.sort ?? ""}
          options={[
            { value: "", label: t("browse.sortScore") },
            { value: "new", label: t("browse.sortNew") },
          ]}
          onChange={(e) => set("sort", e.target.value)}
        />
        <label className="check">
          <input
            type="checkbox"
            checked={draft.exclude_unverified === "true"}
            onChange={(e) => set("exclude_unverified", e.target.checked ? "true" : "")}
          />
          {t("browse.excludeUnverified")}
        </label>
        <div className="filters__actions">
          <Button type="submit" variant="primary">
            {t("browse.apply")}
          </Button>
          {hasFilters(search) && (
            <Button variant="text" onClick={() => void navigate({ to: "/", search: {} })}>
              {t("browse.clear")}
            </Button>
          )}
        </div>
      </form>

      <div aria-live="polite" className="results-status secondary">
        {q.isSuccess ? t("browse.count", { count: versions.length }) : null}
      </div>
      {q.isPending ? (
        <Spinner showLabel />
      ) : q.isError && versions.length === 0 ? (
        <ErrorCallout error={q.error} />
      ) : versions.length === 0 ? (
        <EmptyState
          message={hasFilters(search) ? t("browse.emptyFiltered") : t("browse.empty")}
          action={
            hasFilters(search)
              ? { label: t("browse.clear"), onClick: () => void navigate({ to: "/", search: {} }) }
              : undefined
          }
        />
      ) : (
        <ol className="version-list">
          {versions.map((v) => (
            <li key={v.id}>
              <VersionSummary
                v={v}
                extra={
                  <Link {...videoLink(v.video)} className="mono">
                    {videoLabel(v.video)}
                  </Link>
                }
              />
            </li>
          ))}
        </ol>
      )}
      {q.hasNextPage && (
        <Button onClick={() => void q.fetchNextPage()} disabled={q.isFetchingNextPage}>
          {q.isFetchingNextPage ? t("common.loading") : t("common.loadMore")}
        </Button>
      )}
      {q.isError && versions.length > 0 && <ErrorCallout error={q.error} />}
    </section>
  );
}
