// 投票、檢舉、下架申請與版主的隱藏（HREP-R13～R15、HACC-R15）。
// API 沒有「我投過的票」：按鈕的按下狀態只反映這個頁面上剛投的票（見 docs/ui/README.md 10.17）。
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { isApiError } from "../../api/errors";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import { hasPermission, type VersionView } from "../../api/types";
import { Button, ConfirmDialog, ErrorCallout, SelectField, useToast } from "../../components";
import { formatNumber } from "../../i18n";
import { useMe } from "../../shell/useMe";

export const REPORT_REASONS = [
  "wrong_language",
  "bad_quality",
  "spam",
  "offensive",
  "other",
] as const;

export function Community({ version }: { version: VersionView }) {
  const { t } = useTranslation();
  const me = useMe();
  const qc = useQueryClient();
  const toast = useToast();
  const [mine, setMine] = useState<0 | 1 | -1>(0);
  const [score, setScore] = useState<number | undefined>(undefined);
  const [reportOpen, setReportOpen] = useState(false);
  const [reason, setReason] = useState<string>("wrong_language");
  const [note, setNote] = useState("");
  const [hideOpen, setHideOpen] = useState(false);
  const signedIn = Boolean(me.data);
  const canModerate = hasPermission(me.data, "moderate");
  const visible = version.state === "visible";
  const path = apiPath("/api/v1/versions/{id}/vote", { id: version.id });

  const vote = useMutation({
    mutationFn: async (value: 1 | -1 | 0) => {
      if (value === 0) {
        await api.delete<"/api/v1/versions/{id}/vote">(path);
        return { value: 0 as const, score: undefined };
      }
      const r = await api.put<"/api/v1/versions/{id}/vote">(path, { body: { value } });
      return { value, score: r.score };
    },
    onSuccess: (r) => {
      setMine(r.value);
      if (r.score !== undefined) setScore(r.score);
      void qc.invalidateQueries({ queryKey: queryKeys.version(version.id) });
      void qc.invalidateQueries({ queryKey: ["versions"] });
      void qc.invalidateQueries({ queryKey: ["video"] });
    },
  });
  const report = useMutation({
    mutationFn: () =>
      api.post<"/api/v1/versions/{id}/reports">(
        apiPath("/api/v1/versions/{id}/reports", { id: version.id }),
        {
          body: { reason, note: note.trim() || undefined },
        },
      ),
    onSuccess: () => {
      setReportOpen(false);
      setNote("");
      toast.show({ title: t("community.reported") });
    },
  });
  const hide = useMutation({
    mutationFn: () =>
      api.post<"/api/v1/admin/versions/{id}/hide">(
        apiPath("/api/v1/admin/versions/{id}/hide", { id: version.id }),
      ),
    onSuccess: () => {
      setHideOpen(false);
      toast.show({ title: t("community.hidden") });
      void qc.invalidateQueries({ queryKey: queryKeys.version(version.id) });
    },
  });
  const reportErrs = fieldErrorsOf(report.error);
  const useTakedown = isApiError(report.error) && report.error.code === "use_takedown";

  return (
    <>
      <section aria-labelledby="vote-title">
        <h2 id="vote-title">{t("community.vote")}</h2>
        <p className="num" aria-live="polite">
          {t("community.score", { score: formatNumber(score ?? version.score) })}
        </p>
        {signedIn && visible ? (
          <div className="vote" role="group" aria-label={t("community.vote")}>
            <Button
              aria-pressed={mine === 1}
              disabled={vote.isPending}
              onClick={() => vote.mutate(mine === 1 ? 0 : 1)}
            >
              {t("community.up")}
            </Button>
            <Button
              aria-pressed={mine === -1}
              disabled={vote.isPending}
              onClick={() => vote.mutate(mine === -1 ? 0 : -1)}
            >
              {t("community.down")}
            </Button>
            <Button variant="text" disabled={vote.isPending} onClick={() => vote.mutate(0)}>
              {t("community.clear")}
            </Button>
          </div>
        ) : (
          <p className="secondary">
            {signedIn ? t("community.notVisible") : t("community.signInToVote")}
          </p>
        )}
        {vote.isError && <ErrorCallout error={vote.error} />}
      </section>
      <section aria-labelledby="report-title">
        <h2 id="report-title">{t("community.problems")}</h2>
        <div className="stack-inline">
          {signedIn && visible && (
            <Button onClick={() => setReportOpen(true)}>{t("community.report")}</Button>
          )}
          <Link to="/takedown" search={{ version: version.id }} className="button button--text">
            {t("community.takedown")}
          </Link>
          {canModerate && visible && (
            <Button variant="danger" onClick={() => setHideOpen(true)}>
              {t("community.hide")}
            </Button>
          )}
        </div>
      </section>
      <ConfirmDialog
        open={reportOpen}
        onOpenChange={(o) => {
          setReportOpen(o);
          if (!o) report.reset();
        }}
        title={t("community.reportTitle")}
        description={t("community.reportHint")}
        confirmLabel={t("community.reportSubmit")}
        pending={report.isPending}
        error={useTakedown ? undefined : Object.keys(reportErrs).length ? undefined : report.error}
        onConfirm={() => report.mutate()}
      >
        <SelectField
          label={t("community.reason")}
          value={reason}
          options={REPORT_REASONS.map((r) => ({ value: r, label: t(`reportReason.${r}`) }))}
          onChange={(e) => setReason(e.target.value)}
          error={reportErrs.reason}
        />
        <div className="field">
          <label className="field__label" htmlFor="report-note">
            {t("community.note")}
          </label>
          <textarea
            id="report-note"
            className="field__input"
            rows={4}
            maxLength={1000}
            value={note}
            aria-invalid={reportErrs.note ? true : undefined}
            onChange={(e) => setNote(e.target.value)}
          />
          {reportErrs.note && <span className="field__error">{reportErrs.note}</span>}
        </div>
        {useTakedown && (
          <p className="notice">
            {t("community.useTakedown")}{" "}
            <Link to="/takedown" search={{ version: version.id }}>
              {t("community.takedown")}
            </Link>
          </p>
        )}
      </ConfirmDialog>
      <ConfirmDialog
        open={hideOpen}
        onOpenChange={setHideOpen}
        title={t("community.hideTitle")}
        description={t("community.hideHint")}
        confirmLabel={t("community.hide")}
        danger
        pending={hide.isPending}
        error={hide.error}
        onConfirm={() => hide.mutate()}
      />
    </>
  );
}
