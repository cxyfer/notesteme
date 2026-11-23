// 管理頁的外框：分頁（檢舉、下架申請需要 moderate；使用者、稽核紀錄需要 admin）與權限檢查。
// 權限只決定入口與提示；真正的檢查在 Hub（HACC-R13）。
import { Link, Navigate } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { hasPermission, type Me, type Permission } from "../../api/types";
import { ErrorCallout, Spinner } from "../../components";
import { useMe } from "../../shell/useMe";
import { usePageTitle } from "../../shell/usePageTitle";

const TABS = [
  { to: "/admin/reports", key: "reports", need: "moderate" },
  { to: "/admin/takedowns", key: "takedowns", need: "moderate" },
  { to: "/admin/users", key: "users", need: "admin" },
  { to: "/admin/audit", key: "audit", need: "admin" },
] as const;

export function AdminFrame({
  tab,
  need,
  children,
}: {
  tab: (typeof TABS)[number]["key"];
  need: Permission;
  children: (me: Me) => ReactNode;
}) {
  const { t } = useTranslation();
  const title = t(`admin.tabs.${tab}`);
  usePageTitle(title);
  const me = useMe();
  if (me.isPending) return <Spinner showLabel />;
  if (me.isError) return <ErrorCallout error={me.error} />;
  if (!me.data) return <Navigate to="/login" search={{ redirect: `/admin/${tab}` }} replace />;
  const user = me.data;
  return (
    <section aria-labelledby="admin-title">
      <h1 id="admin-title" className="page__title">
        {title}
      </h1>
      <nav aria-label={t("admin.nav")}>
        <ul className="tabs">
          {TABS.filter((x) => hasPermission(user, x.need)).map((x) => (
            <li key={x.key}>
              <Link to={x.to} aria-current={x.key === tab ? "page" : undefined}>
                {t(`admin.tabs.${x.key}`)}
              </Link>
            </li>
          ))}
        </ul>
      </nav>
      {hasPermission(user, need) ? (
        children(user)
      ) : (
        <ErrorCallout
          error={null}
          message={t("admin.forbidden", { permission: t(`permission.${need}`) })}
        />
      )}
    </section>
  );
}

/** 依狀態篩選的下拉選單 */
export function StateFilter({
  value,
  states,
  prefix,
  onChange,
}: {
  value: string;
  states: readonly string[];
  prefix: string;
  onChange: (v: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <label className="inline-field">
      <span>{t("admin.state")}</span>
      <select value={value} onChange={(e) => onChange(e.target.value)}>
        {states.map((s) => (
          <option key={s} value={s}>
            {t(`${prefix}.${s}`)}
          </option>
        ))}
      </select>
    </label>
  );
}

/** 決定的說明欄（note，最多 2,000 字） */
export function NoteField({
  id,
  value,
  onChange,
  error,
  max = 2000,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  error?: string;
  max?: number;
}) {
  const { t } = useTranslation();
  return (
    <div className="field">
      <label className="field__label" htmlFor={id}>
        {t("admin.note")}
      </label>
      <textarea
        id={id}
        className="field__input"
        rows={3}
        maxLength={max}
        value={value}
        aria-invalid={error ? true : undefined}
        onChange={(e) => onChange(e.target.value)}
      />
      {error && <span className="field__error">{error}</span>}
    </div>
  );
}
