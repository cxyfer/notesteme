// 檢舉的處理（HREP-R15，moderate）：依狀態列出，未處理的可以駁回（恢復隱藏的版本）或移除版本；也可以只隱藏版本。
// 檢舉者只有版主看得到；理由與說明是使用者輸入的文字。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import type { ReportView } from "../../api/types";
import {
  Button,
  ConfirmDialog,
  EmptyState,
  ErrorCallout,
  Spinner,
  useToast,
} from "../../components";
import { formatDateTime } from "../../i18n";
import { AdminFrame, NoteField, StateFilter } from "./AdminFrame";

const STATES = ["open", "dismissed", "actioned"] as const;
type Decision = "dismiss" | "remove";

export function ReportsPage() {
  return (
    <AdminFrame tab="reports" need="moderate">
      {() => <Reports />}
    </AdminFrame>
  );
}

function Reports() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const [state, setState] = useState<string>("open");
  const [deciding, setDeciding] = useState<{ r: ReportView; d: Decision } | null>(null);
  const [note, setNote] = useState("");
  const q = useQuery({
    queryKey: queryKeys.adminReports(state),
    queryFn: () => api.get("/api/v1/admin/reports", { query: { state, limit: 200 } }),
  });
  const decide = useMutation({
    mutationFn: ({ r, d }: { r: ReportView; d: Decision }) =>
      api.post<"/api/v1/admin/reports/{id}/decision">(
        apiPath("/api/v1/admin/reports/{id}/decision", { id: r.id }),
        {
          body: { decision: d, note: note.trim() || undefined },
        },
      ),
    onSuccess: (_, { d }) => {
      setDeciding(null);
      setNote("");
      toast.show({ title: t(d === "dismiss" ? "reports.dismissed" : "reports.removed") });
      void qc.invalidateQueries({ queryKey: ["admin", "reports"] });
      void qc.invalidateQueries({ queryKey: ["version"] });
    },
  });
  const reports = q.data?.reports ?? [];
  const open = (r: ReportView, d: Decision) => {
    decide.reset();
    setNote("");
    setDeciding({ r, d });
  };
  return (
    <>
      <StateFilter value={state} states={STATES} prefix="reportState" onChange={setState} />
      {q.isPending ? (
        <Spinner showLabel />
      ) : q.isError ? (
        <ErrorCallout error={q.error} />
      ) : reports.length === 0 ? (
        <EmptyState message={t("reports.empty")} />
      ) : (
        <ul className="rows">
          {reports.map((r) => (
            <li key={r.id} className="case">
              <div className="case__head">
                <strong>{t(`reportReason.${r.reason}`, { defaultValue: r.reason })}</strong>
                <span className="badge">
                  {t(`reportState.${r.state}`, { defaultValue: r.state })}
                </span>
                <span className="secondary">{formatDateTime(r.created_at)}</span>
              </div>
              <p>
                <Link to="/versions/$id" params={{ id: r.version_id }} className="mono">
                  {t("reports.version", { id: r.version_id })}
                </Link>
              </p>
              <p className="secondary">
                {t("reports.reporter", { name: r.reporter.display_name })}
              </p>
              {r.note && <p className="user-text">{r.note}</p>}
              {r.state === "open" && (
                <div className="stack-inline">
                  <Button onClick={() => open(r, "dismiss")}>{t("reports.dismiss")}</Button>
                  <Button variant="danger" onClick={() => open(r, "remove")}>
                    {t("reports.remove")}
                  </Button>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      <ConfirmDialog
        open={deciding !== null}
        onOpenChange={(o) => !o && setDeciding(null)}
        title={t(deciding?.d === "remove" ? "reports.removeTitle" : "reports.dismissTitle")}
        description={t(deciding?.d === "remove" ? "reports.removeHint" : "reports.dismissHint")}
        confirmLabel={t(deciding?.d === "remove" ? "reports.remove" : "reports.dismiss")}
        danger={deciding?.d === "remove"}
        pending={decide.isPending}
        error={fieldErrorsOf(decide.error).note ? undefined : decide.error}
        onConfirm={() => deciding && decide.mutate(deciding)}
      >
        <NoteField
          id="report-decision-note"
          value={note}
          onChange={setNote}
          error={fieldErrorsOf(decide.error).note}
        />
      </ConfirmDialog>
    </>
  );
}
