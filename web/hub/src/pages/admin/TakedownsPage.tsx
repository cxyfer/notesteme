// 下架申請的處理（HACC-R16，moderate）：移除版本或駁回。聯絡 email 只有版主看得到（解不開時是 null）。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import type { TakedownView } from "../../api/types";
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

const STATES = ["open", "removed", "rejected"] as const;
type Decision = "remove" | "reject";

export function TakedownsPage() {
  return (
    <AdminFrame tab="takedowns" need="moderate">
      {() => <Takedowns />}
    </AdminFrame>
  );
}

function Takedowns() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const [state, setState] = useState<string>("open");
  const [deciding, setDeciding] = useState<{ td: TakedownView; d: Decision } | null>(null);
  const [note, setNote] = useState("");
  const q = useQuery({
    queryKey: queryKeys.adminTakedowns(state),
    queryFn: () => api.get("/api/v1/admin/takedowns", { query: { state, limit: 200 } }),
  });
  const decide = useMutation({
    mutationFn: ({ td, d }: { td: TakedownView; d: Decision }) =>
      api.post<"/api/v1/admin/takedowns/{id}/decision">(
        apiPath("/api/v1/admin/takedowns/{id}/decision", { id: td.id }),
        {
          body: { decision: d, note: note.trim() || undefined },
        },
      ),
    onSuccess: (_, { d }) => {
      setDeciding(null);
      setNote("");
      toast.show({ title: t(d === "remove" ? "takedowns.removed" : "takedowns.rejected") });
      void qc.invalidateQueries({ queryKey: ["admin", "takedowns"] });
      void qc.invalidateQueries({ queryKey: ["version"] });
    },
  });
  const list = q.data?.takedowns ?? [];
  const noteErr = fieldErrorsOf(decide.error).note;
  return (
    <>
      <StateFilter value={state} states={STATES} prefix="takedownState" onChange={setState} />
      {q.isPending ? (
        <Spinner showLabel />
      ) : q.isError ? (
        <ErrorCallout error={q.error} />
      ) : list.length === 0 ? (
        <EmptyState message={t("takedowns.empty")} />
      ) : (
        <ul className="rows">
          {list.map((td) => (
            <li key={td.id} className="case">
              <div className="case__head">
                <strong>{t(`takedownReason.${td.reason}`, { defaultValue: td.reason })}</strong>
                <span className="badge">
                  {t(`takedownState.${td.state}`, { defaultValue: td.state })}
                </span>
                <span className="secondary">{formatDateTime(td.created_at)}</span>
              </div>
              <p>
                <Link to="/versions/$id" params={{ id: td.version_id }} className="mono">
                  {t("reports.version", { id: td.version_id })}
                </Link>
              </p>
              <p className="user-text">{td.statement}</p>
              <p className="secondary">
                {t("takedowns.contact", {
                  email: td.contact_email ?? t("takedowns.contactUnavailable"),
                })}
              </p>
              {td.note && (
                <p className="secondary user-text">
                  {t("takedowns.decisionNote", { note: td.note })}
                </p>
              )}
              {td.state === "open" && (
                <div className="stack-inline">
                  <Button
                    onClick={() => {
                      decide.reset();
                      setDeciding({ td, d: "reject" });
                    }}
                  >
                    {t("takedowns.reject")}
                  </Button>
                  <Button
                    variant="danger"
                    onClick={() => {
                      decide.reset();
                      setDeciding({ td, d: "remove" });
                    }}
                  >
                    {t("takedowns.remove")}
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
        title={t(deciding?.d === "remove" ? "takedowns.removeTitle" : "takedowns.rejectTitle")}
        description={t(deciding?.d === "remove" ? "takedowns.removeHint" : "takedowns.rejectHint")}
        confirmLabel={t(deciding?.d === "remove" ? "takedowns.remove" : "takedowns.reject")}
        danger={deciding?.d === "remove"}
        pending={decide.isPending}
        error={noteErr ? undefined : decide.error}
        onConfirm={() => deciding && decide.mutate(deciding)}
      >
        <NoteField id="takedown-decision-note" value={note} onChange={setNote} error={noteErr} />
      </ConfirmDialog>
    </>
  );
}
