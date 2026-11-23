// 稽核紀錄（HACC-R17，admin）：依時間倒序。
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import { EmptyState, ErrorCallout, Spinner } from "../../components";
import { formatDateTime } from "../../i18n";
import { AdminFrame } from "./AdminFrame";

export function AuditPage() {
  return (
    <AdminFrame tab="audit" need="admin">
      {() => <Audit />}
    </AdminFrame>
  );
}

function Audit() {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: queryKeys.adminAudit,
    queryFn: () => api.get("/api/v1/admin/audit", { query: { limit: 500 } }),
  });
  if (q.isPending) return <Spinner showLabel />;
  if (q.isError) return <ErrorCallout error={q.error} />;
  const entries = q.data.entries ?? [];
  if (entries.length === 0) return <EmptyState message={t("audit.empty")} />;
  return (
    <table className="table">
      <caption className="visually-hidden">{t("admin.tabs.audit")}</caption>
      <thead>
        <tr>
          <th scope="col">{t("audit.at")}</th>
          <th scope="col">{t("audit.actor")}</th>
          <th scope="col">{t("audit.action")}</th>
          <th scope="col">{t("audit.target")}</th>
        </tr>
      </thead>
      <tbody>
        {entries.map((e, i) => (
          <tr key={`${e.at}-${i}`}>
            <td>{formatDateTime(e.at)}</td>
            <td>{e.actor}</td>
            <td>{t(`auditAction.${e.action.replace(".", "_")}`, { defaultValue: e.action })}</td>
            <td className="mono">
              {e.target_kind}:{e.target_id}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
