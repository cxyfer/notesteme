// 登入頁（HACC-R1、R2）：列出已啟用的登入方式，按下去是瀏覽器導向 /auth/<provider>/start（不經過 fetch）。
// OAuth callback 一律轉到 /（HACC A11），所以登入前的頁面記在 sessionStorage，由 HubShell 取用。
import { useQuery } from "@tanstack/react-query";
import { Link, useSearch } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { api } from "../api/client";
import { queryKeys } from "../api/queryKeys";
import { rememberReturnTo } from "../auth/session";
import { EmptyState, ErrorCallout, Spinner } from "../components";
import { useMe } from "../shell/useMe";
import { usePageTitle } from "../shell/usePageTitle";

export function LoginPage() {
  const { t } = useTranslation();
  usePageTitle(t("login.title"));
  const { redirect } = useSearch({ from: "/login" });
  const me = useMe();
  const providers = useQuery({
    queryKey: queryKeys.providers,
    queryFn: () => api.get("/api/v1/auth/providers"),
  });
  const list = providers.data?.providers ?? [];
  return (
    <div className="login">
      <section className="login__card" aria-labelledby="login-title">
        <h1 id="login-title">{t("login.title")}</h1>
        {me.data ? (
          <p>
            {t("login.already", { name: me.data.display_name })}{" "}
            <Link to="/">{t("nav.browse")}</Link>
          </p>
        ) : null}
        <p className="secondary">{t("login.intro")}</p>
        {providers.isPending ? (
          <Spinner showLabel />
        ) : providers.isError ? (
          <ErrorCallout error={providers.error} />
        ) : list.length === 0 ? (
          <EmptyState message={t("login.none")} />
        ) : (
          <ul className="login__providers">
            {list.map((p) => (
              <li key={p.id}>
                <a
                  className="button button--outline login__provider"
                  href={`/auth/${encodeURIComponent(p.id)}/start`}
                  onClick={() => rememberReturnTo(redirect ?? "/")}
                >
                  {t("login.with", { provider: p.name })}
                </a>
              </li>
            ))}
          </ul>
        )}
        <p className="secondary small">{t("login.noEmailMerge")}</p>
        <p className="small">
          <Link to="/takedown">{t("login.takedown")}</Link>
        </p>
      </section>
    </div>
  );
}
