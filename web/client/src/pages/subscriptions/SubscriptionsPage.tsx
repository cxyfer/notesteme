// 訂閱列表（docs/ui/pages.md §5；SUB-R1、R6、R7、R9、R12、R13）：頻道、平台、檢查頻率、上次與下次檢查、
// 處理方案、新內容（SUB-R14：這個訂閱建立、還沒有標為已讀、不在垃圾桶中的影音數）、最近的錯誤；「新增訂閱」抽屜（含試跑預覽）與 OPML／Takeout 匯入。這個頁面是 lazy route。
// ?subscribe=<網址>（「新增」抽屜的「建立訂閱」）直接打開新增訂閱的抽屜並帶入網址；關閉時從網址移除。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { describeError } from "../../api/describeError";
import { isApiError } from "../../api/errors";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import {
  Button,
  Dialog,
  Drawer,
  EmptyState,
  ErrorCallout,
  Spinner,
  TableScroll,
  useToast,
} from "../../components";
import { formatDateTime, formatNumber } from "../../i18n";
import { usePageTitle } from "../../shell/usePageTitle";
import { SubscriptionFields, SubscriptionPreview, scheduleLabel } from "./SubscriptionForm";
import {
  EMPTY_FORM,
  backfillOf,
  createBody,
  filtersOf,
  formProblems,
  previewableUrl,
  type SubscriptionView,
} from "./subModel";

type ImportResult = components["schemas"]["ImportResult"];

const route = getRouteApi("/app/subscriptions");

export function SubscriptionsPage() {
  const { t } = useTranslation();
  usePageTitle(t("pages.subscriptions.title"));
  const subs = useQuery({
    queryKey: queryKeys.subscriptions.list(),
    queryFn: ({ signal }) => api.get("/api/subscriptions", { signal }),
  });
  const { subscribe } = route.useSearch();
  const navigate = useNavigate();
  const [creatingLocal, setCreating] = useState(false);
  const creating = creatingLocal || subscribe !== undefined;
  const [importing, setImporting] = useState(false);
  function closeCreate() {
    setCreating(false);
    if (subscribe !== undefined) void navigate({ to: "/subscriptions", search: {}, replace: true });
  }
  const list = subs.data?.subscriptions ?? [];
  return (
    <>
      <div className="page__head">
        <h1 className="page__title">{t("pages.subscriptions.title")}</h1>
        <div className="page__actions">
          <Button onClick={() => setImporting(true)}>{t("subs.import.open")}</Button>
          <Button variant="primary" onClick={() => setCreating(true)}>
            {t("subs.new")}
          </Button>
        </div>
      </div>
      <p className="secondary page__lead">{t("subs.lead")}</p>
      {subs.isPending && <Spinner label={t("common.loading")} showLabel />}
      {subs.error && (
        <ErrorCallout
          error={subs.error}
          actions={[{ label: t("common.retry"), onClick: () => void subs.refetch() }]}
        />
      )}
      {subs.data && list.length === 0 && (
        <EmptyState
          message={t("subs.empty")}
          action={{ label: t("subs.new"), onClick: () => setCreating(true) }}
        />
      )}
      {list.length > 0 && <SubscriptionTable list={list} />}
      {creating && (
        <NewSubscriptionDrawer key={subscribe ?? ""} initialUrl={subscribe} onClose={closeCreate} />
      )}
      {importing && <ImportDialog onClose={() => setImporting(false)} />}
    </>
  );
}

function SubscriptionTable({ list }: { list: SubscriptionView[] }) {
  const { t } = useTranslation();
  return (
    <TableScroll label={t("subs.tableCaption")}>
      <table className="data-table">
        <caption className="visually-hidden">{t("subs.tableCaption")}</caption>
        <thead>
          <tr>
            <th scope="col">{t("subs.col.source")}</th>
            <th scope="col">{t("subs.col.platform")}</th>
            <th scope="col">{t("subs.col.schedule")}</th>
            <th scope="col">{t("subs.col.lastChecked")}</th>
            <th scope="col">{t("subs.col.nextCheck")}</th>
            <th scope="col">{t("subs.col.profile")}</th>
            <th scope="col" className="num">
              {t("subs.col.newItems")}
            </th>
            <th scope="col">{t("subs.col.state")}</th>
          </tr>
        </thead>
        <tbody>
          {list.map((s) => (
            <tr key={s.id}>
              <th scope="row">
                <Link to="/subscriptions/$id" params={{ id: s.id }} className="data-table__link">
                  {s.title || s.url}
                </Link>
              </th>
              <td>{t(`platform.${s.platform}`, { defaultValue: s.platform })}</td>
              <td>{scheduleLabel(t, s.schedule)}</td>
              <td className="num">
                {s.last_checked_at ? formatDateTime(s.last_checked_at) : t("subs.never")}
              </td>
              <td className="num">
                {!s.enabled
                  ? t("subs.disabled")
                  : s.next_check_at
                    ? formatDateTime(s.next_check_at)
                    : "—"}
              </td>
              <td>{s.profile ?? t("subs.profileDefault")}</td>
              <td className="num">{formatNumber(s.new_items ?? 0)}</td>
              <td>
                {s.last_error ? (
                  <span className="data-table__problem">
                    {t(`errorClass.${s.last_error_class ?? "permanent"}`, {
                      defaultValue: s.last_error_class ?? "",
                    })}
                    {t("subs.errorSep")}
                    {s.last_error}
                  </span>
                ) : (
                  t("subs.ok")
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableScroll>
  );
}

function NewSubscriptionDrawer({
  initialUrl,
  onClose,
}: {
  initialUrl?: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [form, setForm] = useState(() =>
    initialUrl ? { ...EMPTY_FORM, url: initialUrl } : EMPTY_FORM,
  );
  const create = useMutation({
    mutationFn: () => api.post("/api/subscriptions", { body: createBody(form) }),
    onSuccess: async (s) => {
      await qc.invalidateQueries({ queryKey: queryKeys.subscriptions.all });
      toast.show({ title: t("subs.created", { name: s.title || s.url }) });
      onClose();
      await navigate({ to: "/subscriptions/$id", params: { id: s.id } });
    },
  });
  const fe = fieldErrorsOf(create.error);
  const err = create.error;
  const dupId =
    isApiError(err) && err.code === "duplicate_subscription" ? err.body.subscription_id : "";
  const urlError =
    fe.url ??
    (isApiError(err) &&
    ["unsupported_url", "not_a_collection", "duplicate_subscription"].includes(err.code)
      ? describeError(t, err)
      : undefined);
  const blocked = Object.keys(formProblems(form)).length > 0;
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!form.url.trim() || blocked) return;
    create.mutate();
  }
  const ready = previewableUrl(form.url);
  return (
    <Drawer
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("subs.newTitle")}
      description={t("subs.oneInstance")}
      footer={
        <Button
          variant="primary"
          type="submit"
          form="new-subscription"
          disabled={create.isPending || !form.url.trim() || blocked}
        >
          {create.isPending ? t("subs.creating") : t("subs.create")}
        </Button>
      }
    >
      <form id="new-subscription" onSubmit={submit} noValidate>
        <SubscriptionFields
          form={form}
          onChange={setForm}
          errors={{ ...fe, ...(urlError ? { url: urlError } : {}) }}
          showUrl
        />
        {dupId && (
          <p>
            <Link to="/subscriptions/$id" params={{ id: dupId }} onClick={onClose}>
              {t("subs.openExisting")}
            </Link>
          </p>
        )}
        {err != null && !urlError && Object.keys(fe).length === 0 && <ErrorCallout error={err} />}
      </form>
      <SubscriptionPreview
        body={{ url: form.url.trim(), backfill: backfillOf(form), filters: filtersOf(form) }}
        ready={ready && !blocked}
        notReadyHint={t("subs.preview.needUrl")}
        noneMode={form.backfillMode === "none"}
        onRun={(body, signal) =>
          api.post("/api/subscriptions/preview", {
            body: body as components["schemas"]["PreviewNewInputBody"],
            signal,
          })
        }
      />
    </Drawer>
  );
}

function ImportDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);
  const importFile = useMutation({
    mutationFn: (f: File) => {
      const fd = new FormData();
      fd.append("file", f);
      return api.post("/api/subscriptions/import", { body: fd });
    },
    onSuccess: async (r) => {
      setResult(r);
      await qc.invalidateQueries({ queryKey: queryKeys.subscriptions.all });
    },
  });
  const failed = result?.failed ?? [];
  const dups = result?.duplicates ?? [];
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("subs.import.title")}
      description={t("subs.import.description")}
      footer={
        !result && (
          <Button
            variant="primary"
            disabled={!file || importFile.isPending}
            onClick={() => file && importFile.mutate(file)}
          >
            {importFile.isPending ? t("subs.import.running") : t("subs.import.submit")}
          </Button>
        )
      }
    >
      {!result && (
        <div className="field">
          <label className="field__label" htmlFor="sub-import-file">
            {t("subs.import.file")}
          </label>
          <input
            id="sub-import-file"
            type="file"
            accept=".opml,.xml,.csv,text/xml,text/csv,application/xml"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
          />
        </div>
      )}
      {importFile.error != null && <ErrorCallout error={importFile.error} />}
      {result && (
        <div className="sub-import" role="status">
          <p>{t("subs.import.created", { count: result.created })}</p>
          {dups.length > 0 && (
            <details>
              <summary>{t("subs.import.duplicates", { count: dups.length })}</summary>
              <ul className="sub-import__list">
                {dups.map((u, i) => (
                  <li key={`${u}-${i}`}>
                    <code>{u}</code>
                  </li>
                ))}
              </ul>
            </details>
          )}
          {failed.length > 0 && (
            <>
              <p>{t("subs.import.failed", { count: failed.length })}</p>
              <ul className="sub-import__list">
                {failed.map((f) => (
                  <li key={f.line}>
                    {t("subs.import.line", { line: formatNumber(f.line) })} <code>{f.url}</code>{" "}
                    {f.message}
                  </li>
                ))}
              </ul>
            </>
          )}
        </div>
      )}
    </Dialog>
  );
}
