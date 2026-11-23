// 訂閱的設定欄位（新增抽屜與詳情頁共用）與試跑預覽（SUB-R6）。
import { useQuery } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import type { FieldErrors } from "../../api/fieldErrors";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, ErrorCallout, SelectField, Spinner, TextField } from "../../components";
import { formatDate, formatNumber, formatTimecode } from "../../i18n";
import {
  BACKFILL_MODES,
  LIVE_MODES,
  SCHEDULES,
  SKIP_REASONS,
  formProblems,
  type SubForm,
} from "./subModel";

type Preview = components["schemas"]["Preview"];

export function scheduleLabel(t: TFunction, s: string) {
  return (SCHEDULES as readonly string[]).includes(s)
    ? t(`subs.schedule.${s}`)
    : t("subs.scheduleCustom", { value: s });
}

export function SubscriptionFields({
  form,
  onChange,
  errors,
  showUrl,
}: {
  form: SubForm;
  onChange: (f: SubForm) => void;
  errors: FieldErrors;
  showUrl: boolean;
}) {
  const { t } = useTranslation();
  const set = <K extends keyof SubForm>(k: K, v: SubForm[K]) => onChange({ ...form, [k]: v });
  const profiles = useQuery({
    queryKey: queryKeys.profiles,
    queryFn: ({ signal }) => api.get("/api/profiles", { signal }),
  });
  const profileOptions = [
    { value: "", label: t("subs.profileDefault") },
    ...(profiles.data?.profiles ?? []).map((p) => ({ value: p.name, label: p.name })),
  ];
  if (form.profile && !profileOptions.some((o) => o.value === form.profile))
    profileOptions.push({ value: form.profile, label: form.profile });
  const scheduleOptions: { value: string; label: string }[] = SCHEDULES.map((s) => ({
    value: s,
    label: scheduleLabel(t, s),
  }));
  if (!(SCHEDULES as readonly string[]).includes(form.schedule))
    scheduleOptions.push({ value: form.schedule, label: scheduleLabel(t, form.schedule) });
  const bad = formProblems(form);

  return (
    <div className="form-stack sub-form">
      {showUrl && (
        <TextField
          label={t("subs.url")}
          type="url"
          inputMode="url"
          autoComplete="off"
          required
          value={form.url}
          onChange={(e) => set("url", e.target.value)}
          hint={t("subs.urlHint")}
          error={errors.url}
        />
      )}
      <div className="form-grid">
        <SelectField
          label={t("subs.profile")}
          value={form.profile}
          options={profileOptions}
          onChange={(e) => set("profile", e.target.value)}
          error={errors.profile}
        />
        <SelectField
          label={t("subs.scheduleLabel")}
          value={form.schedule}
          options={scheduleOptions}
          onChange={(e) => set("schedule", e.target.value)}
          error={errors.schedule}
        />
      </div>
      <fieldset className="plain-fieldset">
        <legend>{t("subs.backfill")}</legend>
        {BACKFILL_MODES.map((m) => (
          <label className="check-row" key={m}>
            <input
              type="radio"
              name="backfill-mode"
              value={m}
              checked={form.backfillMode === m}
              onChange={() => set("backfillMode", m)}
            />
            {t(`subs.backfillMode.${m}`)}
          </label>
        ))}
        {form.backfillMode === "recent" && (
          <TextField
            label={t("subs.backfillCount")}
            type="number"
            min={1}
            max={200}
            value={form.backfillCount}
            onChange={(e) => set("backfillCount", e.target.value)}
            error={
              errors["backfill.count"] ?? (bad.count ? t("subs.backfillCountRule") : undefined)
            }
          />
        )}
        {errors["backfill.mode"] && <p className="field__error">{errors["backfill.mode"]}</p>}
      </fieldset>
      <fieldset className="plain-fieldset">
        <legend>{t("subs.filters")}</legend>
        <div className="form-grid">
          <TextField
            label={t("subs.minMinutes")}
            type="number"
            min={0}
            value={form.minMinutes}
            onChange={(e) => set("minMinutes", e.target.value)}
            error={
              errors["filters.min_duration_s"] ??
              (bad.minMinutes ? t("subs.minutesRule") : undefined)
            }
          />
          <TextField
            label={t("subs.maxMinutes")}
            type="number"
            min={0}
            value={form.maxMinutes}
            onChange={(e) => set("maxMinutes", e.target.value)}
            error={
              errors["filters.max_duration_s"] ??
              (bad.maxMinutes ? t("subs.minutesRule") : undefined)
            }
          />
        </div>
        <label className="check-row">
          <input
            type="checkbox"
            checked={form.excludeShorts}
            onChange={(e) => set("excludeShorts", e.target.checked)}
          />
          {t("subs.excludeShorts")}
        </label>
        <SelectField
          label={t("subs.live")}
          value={form.live}
          options={LIVE_MODES.map((m) => ({ value: m, label: t(`subs.liveMode.${m}`) }))}
          onChange={(e) => set("live", e.target.value)}
          error={errors["filters.live"]}
        />
        <div className="form-grid">
          <TextField
            label={t("subs.titleInclude")}
            value={form.titleInclude}
            maxLength={200}
            onChange={(e) => set("titleInclude", e.target.value)}
            hint={t("subs.regexHint")}
            error={errors["filters.title_include"]}
          />
          <TextField
            label={t("subs.titleExclude")}
            value={form.titleExclude}
            maxLength={200}
            onChange={(e) => set("titleExclude", e.target.value)}
            hint={t("subs.regexHint")}
            error={errors["filters.title_exclude"]}
          />
        </div>
      </fieldset>
    </div>
  );
}

const PREVIEW_DEBOUNCE_MS = 800;

/**
 * 試跑預覽：會連到來源網站並消耗 rate limit（SUB-R10），所以不在開啟頁面時自動執行；
 * 使用者按過一次「試跑預覽」之後，修改設定才以 debounce 即時更新，新請求會取消舊的。
 */
export function SubscriptionPreview({
  body,
  ready,
  notReadyHint,
  onRun,
  noneMode,
}: {
  /** 預覽的參數（序列化後當作是否需要重新預覽的依據） */
  body: unknown;
  /** false 時不送出（例如網址還不像網址） */
  ready: boolean;
  notReadyHint: string;
  onRun: (body: unknown, signal: AbortSignal) => Promise<Preview>;
  noneMode: boolean;
}) {
  const { t } = useTranslation();
  const [active, setActive] = useState(false);
  const [result, setResult] = useState<Preview | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  const key = JSON.stringify(body);
  const runRef = useRef(onRun);
  useEffect(() => {
    runRef.current = onRun;
  });

  useEffect(() => {
    if (!active || !ready) return;
    const ctrl = new AbortController();
    const h = setTimeout(() => {
      setLoading(true);
      setError(null);
      runRef
        .current(JSON.parse(key) as unknown, ctrl.signal)
        .then((r) => {
          if (!ctrl.signal.aborted) setResult(r);
        })
        .catch((e: unknown) => {
          if (!ctrl.signal.aborted) setError(e);
        })
        .finally(() => {
          if (!ctrl.signal.aborted) setLoading(false);
        });
    }, PREVIEW_DEBOUNCE_MS);
    return () => {
      clearTimeout(h);
      ctrl.abort();
    };
  }, [active, ready, key]);

  const retryAfter =
    isApiError(error) && error.code === "rate_limited" ? error.body.retry_after_s : undefined;
  return (
    <section className="sub-preview" aria-labelledby="sub-preview-title">
      <h2 id="sub-preview-title" className="tune-section__title">
        {t("subs.preview.title")}
      </h2>
      <p className="secondary">{t("subs.preview.network")}</p>
      {!active && (
        <Button onClick={() => setActive(true)} disabled={!ready}>
          {t("subs.preview.run")}
        </Button>
      )}
      {!ready && <p className="secondary">{notReadyHint}</p>}
      {active && ready && (
        <p className="secondary" aria-live="polite">
          {loading ? <Spinner label={t("subs.preview.loading")} showLabel /> : null}
        </p>
      )}
      {error != null && (
        <ErrorCallout
          error={error}
          live={false}
          message={
            retryAfter != null
              ? t("subs.preview.rateLimited", { seconds: formatNumber(retryAfter) })
              : undefined
          }
          category={retryAfter != null ? "wait" : undefined}
        />
      )}
      {result && <PreviewLists preview={result} noneMode={noneMode} />}
    </section>
  );
}

function PreviewLists({ preview, noneMode }: { preview: Preview; noneMode: boolean }) {
  const { t } = useTranslation();
  const process = preview.would_process ?? [];
  const skip = preview.would_skip ?? [];
  return (
    <div className="sub-preview__lists">
      {noneMode && <p className="secondary">{t("subs.preview.noneMode")}</p>}
      <h3 className="sub-preview__heading">
        {t("subs.preview.wouldProcess", { count: process.length })}
      </h3>
      {process.length > 0 && (
        <ul className="sub-preview__rows">
          {process.map((r) => (
            <PreviewRow key={r.ref} row={r} />
          ))}
        </ul>
      )}
      <h3 className="sub-preview__heading">
        {t("subs.preview.wouldSkip", { count: skip.length })}
      </h3>
      {skip.length > 0 && (
        <ul className="sub-preview__rows">
          {skip.map((r) => (
            <PreviewRow key={r.ref} row={r} reason={r.reason} />
          ))}
        </ul>
      )}
      {preview.truncated && <p className="secondary">{t("subs.preview.truncated")}</p>}
    </div>
  );
}

export function reasonLabel(t: TFunction, reason: string) {
  return (SKIP_REASONS as readonly string[]).includes(reason) ? t(`subs.reason.${reason}`) : reason;
}

function PreviewRow({
  row,
  reason,
}: {
  row: { ref: string; title: string; published_at: string | null; duration_s: number | null };
  reason?: string;
}) {
  const { t } = useTranslation();
  return (
    <li className="sub-preview__row">
      <span className="num secondary">
        {row.published_at ? formatDate(row.published_at) : t("subs.dateUnknown")}
      </span>
      <span className="sub-preview__title">{row.title || row.ref}</span>
      <span className="num secondary">
        {row.duration_s != null ? formatTimecode(row.duration_s) : ""}
      </span>
      {reason && (
        <span className="secondary">
          {t("subs.reasonPrefix", { reason: reasonLabel(t, reason) })}
        </span>
      )}
    </li>
  );
}
