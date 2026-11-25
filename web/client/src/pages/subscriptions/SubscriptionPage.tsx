// 訂閱詳情與編輯（docs/ui/pages.md §5；SUB-R3、R4、R6、R9、R12、R13）：來源、檢查時間、最近的錯誤；
// 處理方案、檢查頻率、回補範圍、過濾條件（PATCH 只送改過的欄位）；試跑預覽；立即檢查、停用、刪除；
// 探索到的條目與略過原因。這個頁面是 lazy route。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { describeError } from "../../api/describeError";
import { isApiError } from "../../api/errors";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import {
  Button,
  Dialog,
  EmptyState,
  ErrorCallout,
  SelectField,
  Spinner,
  TableScroll,
  useToast,
} from "../../components";
import { formatDate, formatDateTime, formatTimecode } from "../../i18n";
import { usePageTitle } from "../../shell/usePageTitle";
import { SubscriptionFields, SubscriptionPreview, reasonLabel } from "./SubscriptionForm";
import {
  backfillOf,
  filtersOf,
  formFromView,
  formProblems,
  patchBody,
  type SubForm,
  type SubscriptionView,
} from "./subModel";

const route = getRouteApi("/app/subscriptions/$id");

export function SubscriptionPage() {
  const { t } = useTranslation();
  const { id } = route.useParams();
  const sub = useQuery({
    queryKey: queryKeys.subscriptions.detail(id),
    queryFn: ({ signal }) =>
      api.get<"/api/subscriptions/{id}">(apiPath("/api/subscriptions/{id}", { id }), { signal }),
  });
  const s = sub.data;
  usePageTitle(s ? s.title || s.url : t("pages.subscriptions.title"));
  return (
    <>
      <nav className="crumbs" aria-label={t("subs.breadcrumb")}>
        <Link to="/subscriptions">{t("pages.subscriptions.title")}</Link>
      </nav>
      {!s ? (
        <>
          <h1 className="page__title">{t("pages.subscriptions.title")}</h1>
          {sub.isPending && <Spinner label={t("common.loading")} showLabel />}
          {sub.error &&
            (isApiError(sub.error) && sub.error.status === 404 ? (
              <EmptyState message={t("subs.notFound")} />
            ) : (
              <ErrorCallout
                error={sub.error}
                actions={[{ label: t("common.retry"), onClick: () => void sub.refetch() }]}
              />
            ))}
        </>
      ) : (
        // key：重新讀取到新版本時重設表單（例如儲存之後）
        <SubscriptionView key={`${s.id}`} s={s} />
      )}
    </>
  );
}

function SubscriptionView({ s }: { s: SubscriptionView }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const saved = formFromView(s);
  const [form, setForm] = useState<SubForm>(saved);
  const [confirm, setConfirm] = useState<"delete" | "disable" | null>(null);
  const detailKey = queryKeys.subscriptions.detail(s.id);
  const path = apiPath("/api/subscriptions/{id}", { id: s.id });

  const save = useMutation({
    mutationFn: () => {
      const body = patchBody(saved, form);
      if (!body) return Promise.resolve(s);
      return api.patch<"/api/subscriptions/{id}">(path, { body });
    },
    onSuccess: async (v) => {
      qc.setQueryData(detailKey, v);
      setForm(formFromView(v));
      await qc.invalidateQueries({ queryKey: queryKeys.subscriptions.list() });
      toast.show({ title: t("common.saved") });
    },
  });
  const setEnabled = useMutation({
    mutationFn: (enabled: boolean) =>
      api.patch<"/api/subscriptions/{id}">(path, { body: { enabled } }),
    onSuccess: async (v) => {
      qc.setQueryData(detailKey, v);
      setConfirm(null);
      await qc.invalidateQueries({ queryKey: queryKeys.subscriptions.list() });
      toast.show({ title: v.enabled ? t("subs.enabledDone") : t("subs.disabledDone") });
    },
  });
  const check = useMutation({
    mutationFn: () =>
      api.post<"/api/subscriptions/{id}/check">(
        apiPath("/api/subscriptions/{id}/check", { id: s.id }),
      ),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: detailKey });
      toast.show({ title: t("subs.checkQueued") });
    },
    onError: (err) => toast.show({ title: describeError(t, err) }),
  });
  const remove = useMutation({
    mutationFn: () => api.delete<"/api/subscriptions/{id}">(path),
    onSuccess: async () => {
      setConfirm(null);
      qc.removeQueries({ queryKey: detailKey });
      await qc.invalidateQueries({ queryKey: queryKeys.subscriptions.list() });
      toast.show({ title: t("subs.deleted") });
      await navigate({ to: "/subscriptions" });
    },
  });

  const fe = fieldErrorsOf(save.error);
  const dirty = patchBody(saved, form) !== undefined;
  const blocked = Object.keys(formProblems(form)).length > 0;
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!dirty || blocked) return;
    save.mutate();
  }
  const name = s.title || s.url;
  return (
    <>
      <div className="page__head">
        <h1 className="page__title">{name}</h1>
        <div className="page__actions">
          <Button onClick={() => check.mutate()} disabled={check.isPending || !s.enabled}>
            {t("subs.checkNow")}
          </Button>
          {s.enabled ? (
            <Button onClick={() => setConfirm("disable")}>{t("subs.disable")}</Button>
          ) : (
            <Button onClick={() => setEnabled.mutate(true)} disabled={setEnabled.isPending}>
              {t("subs.enable")}
            </Button>
          )}
          <Button variant="danger" onClick={() => setConfirm("delete")}>
            {t("subs.delete")}
          </Button>
        </div>
      </div>
      <dl className="meta-list">
        <div>
          <dt>{t("subs.url")}</dt>
          <dd>
            <a href={s.url} target="_blank" rel="noreferrer noopener" className="break-anywhere">
              {s.url}
            </a>
          </dd>
        </div>
        <div>
          <dt>{t("subs.col.platform")}</dt>
          <dd>
            {t(`platform.${s.platform}`, { defaultValue: s.platform })} ·{" "}
            {t(`subs.kind.${s.kind}`, { defaultValue: s.kind })}
          </dd>
        </div>
        <div>
          <dt>{t("subs.col.lastChecked")}</dt>
          <dd className="num">
            {s.last_checked_at ? formatDateTime(s.last_checked_at) : t("subs.never")}
          </dd>
        </div>
        <div>
          <dt>{t("subs.col.nextCheck")}</dt>
          <dd className="num">
            {!s.enabled
              ? t("subs.disabled")
              : s.next_check_at
                ? formatDateTime(s.next_check_at)
                : "—"}
          </dd>
        </div>
      </dl>
      {s.last_error && (
        <ErrorCallout
          error={null}
          live={false}
          category={s.last_error_class === "retryable" ? "retryable" : "needs_action"}
          message={s.last_error}
        />
      )}

      <section className="tune-section" aria-labelledby="sub-settings-title">
        <h2 id="sub-settings-title" className="tune-section__title">
          {t("subs.settingsTitle")}
        </h2>
        <form onSubmit={submit} noValidate>
          <SubscriptionFields form={form} onChange={setForm} errors={fe} showUrl={false} />
          {save.error != null && Object.keys(fe).length === 0 && (
            <ErrorCallout error={save.error} />
          )}
          <div className="page__actions page__actions--start">
            <Button variant="primary" type="submit" disabled={!dirty || blocked || save.isPending}>
              {save.isPending ? t("common.saving") : t("common.saveChanges")}
            </Button>
            {dirty && (
              <Button variant="text" onClick={() => setForm(saved)}>
                {t("common.discard")}
              </Button>
            )}
          </div>
          {backfillOf(saved).mode !== backfillOf(form).mode && (
            <p className="secondary">{t("subs.backfillChangeHint")}</p>
          )}
          <p className="secondary">{t("subs.filtersHint")}</p>
        </form>
      </section>

      <SubscriptionPreview
        body={{ backfill: backfillOf(form), filters: filtersOf(form) }}
        ready={!blocked}
        notReadyHint={t("subs.preview.fixFields")}
        noneMode={form.backfillMode === "none"}
        onRun={(body, signal) =>
          api.post<"/api/subscriptions/{id}/preview">(
            apiPath("/api/subscriptions/{id}/preview", { id: s.id }),
            { body: body as never, signal },
          )
        }
      />

      <Entries id={s.id} />

      <Dialog
        open={confirm === "delete"}
        onOpenChange={(o) => !o && setConfirm(null)}
        title={t("subs.deleteTitle", { name })}
        description={t("subs.deleteDescription")}
        footer={
          <Button variant="danger" onClick={() => remove.mutate()} disabled={remove.isPending}>
            {t("subs.delete")}
          </Button>
        }
      >
        {remove.error != null && <ErrorCallout error={remove.error} />}
      </Dialog>
      <Dialog
        open={confirm === "disable"}
        onOpenChange={(o) => !o && setConfirm(null)}
        title={t("subs.disableTitle", { name })}
        description={t("subs.disableDescription")}
        footer={
          <Button
            variant="danger"
            onClick={() => setEnabled.mutate(false)}
            disabled={setEnabled.isPending}
          >
            {t("subs.disable")}
          </Button>
        }
      >
        {setEnabled.error != null && <ErrorCallout error={setEnabled.error} />}
      </Dialog>
    </>
  );
}

const ENTRY_PAGE = 200;
const ENTRY_MAX = 5000;
const DECISIONS = ["", "created", "skipped", "deferred"] as const;

/** 探索到的條目（GET /entries）：API 沒有 cursor，「載入更多」改成提高 limit（上限 5000）。 */
function Entries({ id }: { id: string }) {
  const { t } = useTranslation();
  const [decision, setDecision] = useState<(typeof DECISIONS)[number]>("");
  const [limit, setLimit] = useState(ENTRY_PAGE);
  const entries = useQuery({
    queryKey: [...queryKeys.subscriptions.entries(id, decision), limit],
    queryFn: ({ signal }) =>
      api.get<"/api/subscriptions/{id}/entries">(
        apiPath("/api/subscriptions/{id}/entries", { id }),
        { query: { decision: decision || undefined, limit }, signal },
      ),
    placeholderData: (prev) => prev,
  });
  const list = entries.data?.entries ?? [];
  return (
    <section className="tune-section" aria-labelledby="sub-entries-title">
      <h2 id="sub-entries-title" className="tune-section__title">
        {t("subs.entries.title")}
      </h2>
      <SelectField
        label={t("subs.entries.filter")}
        value={decision}
        options={DECISIONS.map((d) => ({
          value: d,
          label: d ? t(`subs.decision.${d}`) : t("subs.entries.all"),
        }))}
        onChange={(e) => {
          setDecision(e.target.value as (typeof DECISIONS)[number]);
          setLimit(ENTRY_PAGE);
        }}
      />
      {entries.isPending && <Spinner label={t("common.loading")} showLabel />}
      {entries.error != null && (
        <ErrorCallout
          error={entries.error}
          actions={[{ label: t("common.retry"), onClick: () => void entries.refetch() }]}
        />
      )}
      {entries.data && list.length === 0 && <p className="secondary">{t("subs.entries.empty")}</p>}
      {list.length > 0 && (
        <TableScroll label={t("subs.entries.title")}>
          <table className="data-table">
            <caption className="visually-hidden">{t("subs.entries.title")}</caption>
            <thead>
              <tr>
                <th scope="col">{t("subs.entries.published")}</th>
                <th scope="col">{t("subs.entries.titleCol")}</th>
                <th scope="col">{t("subs.entries.duration")}</th>
                <th scope="col">{t("subs.entries.decision")}</th>
              </tr>
            </thead>
            <tbody>
              {list.map((e) => (
                <tr key={`${e.ref}-${e.part}`}>
                  <td className="num">
                    {e.published_at ? formatDate(e.published_at) : t("subs.dateUnknown")}
                  </td>
                  <td>
                    {e.item_id ? (
                      <Link to="/items/$id" params={{ id: e.item_id }}>
                        {e.title || e.ref}
                      </Link>
                    ) : (
                      e.title || e.ref
                    )}
                  </td>
                  <td className="num">
                    {e.duration_s != null ? formatTimecode(e.duration_s) : ""}
                  </td>
                  <td>
                    {t(`subs.decision.${e.decision}`)}
                    {e.reason ? t("subs.errorSep") + reasonLabel(t, e.reason) : ""}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      {list.length >= limit && limit < ENTRY_MAX && (
        <Button
          onClick={() => setLimit((l) => Math.min(ENTRY_MAX, l + ENTRY_PAGE))}
          disabled={entries.isFetching}
        >
          {entries.isFetching ? t("common.loadingMore") : t("common.loadMore")}
        </Button>
      )}
    </section>
  );
}
