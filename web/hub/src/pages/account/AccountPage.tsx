// 帳號頁（HACC-R6、R7、R9，HUB-01）：登入方式的連結與移除、Hub token 的建立與撤銷、我的分享（有 upload 時）。
// 沒有登入時導到登入頁。
import { Navigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { hasPermission } from "../../api/types";
import { ErrorCallout, Spinner } from "../../components";
import { useMe } from "../../shell/useMe";
import { usePageTitle } from "../../shell/usePageTitle";
import { Identities } from "./Identities";
import { MyShares } from "./MyShares";
import { Tokens } from "./Tokens";

export function AccountPage() {
  const { t } = useTranslation();
  usePageTitle(t("account.title"));
  const me = useMe();
  if (me.isPending) return <Spinner showLabel />;
  if (me.isError) return <ErrorCallout error={me.error} />;
  if (!me.data) return <Navigate to="/login" search={{ redirect: "/account" }} replace />;
  const user = me.data;
  const canUpload = hasPermission(user, "upload");
  return (
    <div className="account">
      <h1 className="page__title">{t("account.title")}</h1>
      <section aria-labelledby="profile-title" className="panel">
        <h2 id="profile-title">{t("account.profile")}</h2>
        <dl className="facts">
          <div>
            <dt>{t("account.name")}</dt>
            <dd>{user.display_name}</dd>
          </div>
          <div>
            <dt>{t("account.groups")}</dt>
            <dd>{(user.groups ?? []).join(t("common.listSep"))}</dd>
          </div>
          <div>
            <dt>{t("account.permissions")}</dt>
            <dd>
              {(user.permissions ?? [])
                .map((p) => t(`permission.${p}`, { defaultValue: p }))
                .join(t("common.listSep"))}
            </dd>
          </div>
        </dl>
      </section>
      <Identities me={user} />
      <Tokens me={user} />
      {canUpload && <MyShares />}
    </div>
  );
}
