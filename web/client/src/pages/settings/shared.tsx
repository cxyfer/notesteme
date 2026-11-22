// 設定分區共用的小元件與 hook（只在設定頁用到，所以不放進 components/）。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import { ErrorCallout, Spinner } from "../../components";

/** 分區裡的一個區塊（h3 標題＋內容）。 */
export function Panel({
  title,
  description,
  children,
}: {
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <div className="settings-panel">
      <h3 className="settings-panel__title">{title}</h3>
      {description && <p className="settings-panel__description">{description}</p>}
      {children}
    </div>
  );
}

/** 後端還沒有提供、或之後的里程碑才提供的功能：明確說明，不放假的表單。 */
export function NotAvailable({ children }: { children: ReactNode }) {
  return (
    <p className="settings-note" data-kind="unavailable">
      {children}
    </p>
  );
}

/** 422 回應的逐欄位錯誤（errors[].path／message）；沒有的時候用 ErrorCallout 顯示整體錯誤。 */
export function SaveError({ error }: { error: unknown }) {
  const { t } = useTranslation();
  const fields = isApiError(error) ? (error.body.errors ?? []) : [];
  if (fields.length === 0) return <ErrorCallout error={error} />;
  return (
    <div className="callout" role="alert" data-category="needs_action">
      <p className="callout__category">{t("settings.common.fieldErrors")}</p>
      <ul className="settings-errors">
        {fields.map((f, i) => (
          <li key={`${f.path}-${i}`}>
            <code>{f.path}</code> {f.message}
          </li>
        ))}
      </ul>
    </div>
  );
}

export function Loading() {
  const { t } = useTranslation();
  return (
    <p className="settings-note">
      <Spinner label={t("common.loading")} showLabel />
    </p>
  );
}

type SectionBody = Record<string, unknown>;

/** GET／PUT /api/settings/{section}（整段取代；呼叫端負責送出完整的區段）。 */
export function useSettingsSection(section: string) {
  const qc = useQueryClient();
  const query = useQuery({
    queryKey: queryKeys.settings(section),
    queryFn: () =>
      api.get<"/api/settings/{section}">(
        apiPath("/api/settings/{section}", { section }),
      ) as Promise<SectionBody>,
  });
  async function save(body: SectionBody) {
    const out = (await api.put<"/api/settings/{section}">(
      apiPath("/api/settings/{section}", { section }),
      {
        body,
      },
    )) as SectionBody;
    qc.setQueryData(queryKeys.settings(section), out);
    return out;
  }
  return { query, save };
}
