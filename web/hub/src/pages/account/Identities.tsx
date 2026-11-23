// 登入方式（HACC-R6、R7）：連結是瀏覽器導向供應商（POST …/link 回 authorize_url），完成後 callback 回到 /account。
// 最近一次登入超過 10 分鐘時 403 reauth_required：提示重新登入。只剩一個登入方式時不能移除（409 last_identity）。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import type { Me } from "../../api/types";
import { Button, ConfirmDialog, ErrorCallout, useToast } from "../../components";

/** 導向外部的 authorize URL（測試時替換） */
export const browser = {
  assign(url: string) {
    window.location.assign(url);
  },
};

export function Identities({ me }: { me: Me }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [removing, setRemoving] = useState<string | null>(null);
  const providers = useQuery({
    queryKey: queryKeys.providers,
    queryFn: () => api.get("/api/v1/auth/providers"),
  });
  const names = new Map((providers.data?.providers ?? []).map((p) => [p.id, p.name]));
  const name = (id: string) => names.get(id) ?? id;
  const linked = new Set((me.identities ?? []).map((i) => i.provider));
  const linkable = (providers.data?.providers ?? []).filter((p) => !linked.has(p.id));

  const link = useMutation({
    mutationFn: (provider: string) =>
      api.post<"/api/v1/me/identities/{provider}/link">(
        apiPath("/api/v1/me/identities/{provider}/link", { provider }),
      ),
    onSuccess: (r) => {
      // 只接受 https（authorize URL 由 Hub 產生，仍然防一下 javascript: 之類的值）
      if (/^https:\/\//i.test(r.authorize_url)) browser.assign(r.authorize_url);
    },
  });
  const unlink = useMutation({
    mutationFn: (provider: string) =>
      api.delete<"/api/v1/me/identities/{provider}">(
        apiPath("/api/v1/me/identities/{provider}", { provider }),
      ),
    onSuccess: () => {
      setRemoving(null);
      toast.show({ title: t("identities.removed") });
      void qc.invalidateQueries({ queryKey: queryKeys.me });
    },
  });
  const reauth = isApiError(link.error) && link.error.code === "reauth_required";

  return (
    <section aria-labelledby="identities-title" className="panel">
      <h2 id="identities-title">{t("identities.title")}</h2>
      <ul className="rows">
        {(me.identities ?? []).map((i) => (
          <li key={i.provider} className="row">
            <span>
              <strong>{name(i.provider)}</strong> <span className="secondary">{i.login}</span>
            </span>
            <Button
              variant="text"
              onClick={() => {
                unlink.reset();
                setRemoving(i.provider);
              }}
            >
              {t("identities.remove", { provider: name(i.provider) })}
            </Button>
          </li>
        ))}
      </ul>
      {linkable.length > 0 && (
        <div className="stack-inline">
          {linkable.map((p) => (
            <Button key={p.id} disabled={link.isPending} onClick={() => link.mutate(p.id)}>
              {t("identities.link", { provider: p.name })}
            </Button>
          ))}
        </div>
      )}
      <p className="secondary small">{t("identities.hint")}</p>
      {link.isError &&
        (reauth ? (
          <ErrorCallout
            error={link.error}
            message={t("identities.reauth")}
            actions={[
              {
                label: t("identities.signInAgain"),
                primary: true,
                onClick: () => void navigate({ to: "/login", search: { redirect: "/account" } }),
              },
            ]}
          />
        ) : (
          <ErrorCallout error={link.error} />
        ))}
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(o) => !o && setRemoving(null)}
        title={t("identities.removeTitle", { provider: removing ? name(removing) : "" })}
        description={t("identities.removeHint")}
        confirmLabel={t("identities.removeConfirm")}
        danger
        pending={unlink.isPending}
        error={unlink.error}
        onConfirm={() => removing && unlink.mutate(removing)}
      />
    </section>
  );
}
