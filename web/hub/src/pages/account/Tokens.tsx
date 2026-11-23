// Hub token（PAT，HACC-R9）：給 Client 與 MCP 用。只在建立時顯示一次；權限只能從帳號目前有的權限中選。
// 撤銷是不可復原的操作，先確認。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import { PERMISSIONS, type Me, type TokenView } from "../../api/types";
import {
  Button,
  ConfirmDialog,
  CopyButton,
  Dialog,
  EmptyState,
  ErrorCallout,
  Spinner,
  TextField,
  useToast,
} from "../../components";
import { formatDate, formatDateTime } from "../../i18n";

export function Tokens({ me }: { me: Me }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const held = PERMISSIONS.filter((p) => (me.permissions ?? []).includes(p));
  const [name, setName] = useState("");
  const [perms, setPerms] = useState<string[]>(
    held.filter((p) => p === "read" || p === "download"),
  );
  const [days, setDays] = useState("90");
  const [revoking, setRevoking] = useState<TokenView | null>(null);
  const [now] = useState(() => Date.now());
  const list = useQuery({
    queryKey: queryKeys.tokens,
    queryFn: () => api.get("/api/v1/me/tokens"),
  });
  const create = useMutation({
    mutationFn: () =>
      api.post("/api/v1/me/tokens", {
        body: { name: name.trim(), permissions: perms, expires_in_days: Number(days) },
      }),
    onSuccess: () => {
      setName("");
      void qc.invalidateQueries({ queryKey: queryKeys.tokens });
    },
  });
  const revoke = useMutation({
    mutationFn: (id: string) =>
      api.delete<"/api/v1/me/tokens/{id}">(apiPath("/api/v1/me/tokens/{id}", { id })),
    onSuccess: () => {
      setRevoking(null);
      toast.show({ title: t("tokens.revoked") });
      void qc.invalidateQueries({ queryKey: queryKeys.tokens });
    },
  });
  const errs = fieldErrorsOf(create.error);
  const permErr = Object.entries(errs).find(([k]) => k.startsWith("permissions"))?.[1];
  const submit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };
  const tokens = list.data?.tokens ?? [];

  return (
    <section aria-labelledby="tokens-title" className="panel">
      <h2 id="tokens-title">{t("tokens.title")}</h2>
      <p className="secondary">{t("tokens.intro")}</p>
      {list.isPending ? (
        <Spinner showLabel />
      ) : list.isError ? (
        <ErrorCallout error={list.error} />
      ) : tokens.length === 0 ? (
        <EmptyState message={t("tokens.empty")} />
      ) : (
        <table className="table">
          <caption className="visually-hidden">{t("tokens.title")}</caption>
          <thead>
            <tr>
              <th scope="col">{t("tokens.name")}</th>
              <th scope="col">{t("tokens.permissions")}</th>
              <th scope="col">{t("tokens.created")}</th>
              <th scope="col">{t("tokens.expires")}</th>
              <th scope="col">
                <span className="visually-hidden">{t("common.actions")}</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {tokens.map((tk) => (
              <tr key={tk.id}>
                <td>{tk.name}</td>
                <td>
                  {(tk.permissions ?? [])
                    .map((p) => t(`permission.${p}`, { defaultValue: p }))
                    .join(t("common.listSep"))}
                </td>
                <td>{formatDateTime(tk.created_at)}</td>
                <td>
                  {formatDate(tk.expires_at)}
                  {Date.parse(tk.expires_at) <= now && (
                    <span className="badge">{t("tokens.expired")}</span>
                  )}
                </td>
                <td>
                  <Button
                    variant="text"
                    onClick={() => {
                      revoke.reset();
                      setRevoking(tk);
                    }}
                  >
                    {t("tokens.revoke", { name: tk.name })}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <form className="stack token-form" onSubmit={submit} aria-labelledby="token-new-title">
        <h3 id="token-new-title">{t("tokens.new")}</h3>
        <TextField
          label={t("tokens.name")}
          value={name}
          maxLength={100}
          onChange={(e) => setName(e.target.value)}
          error={errs.name}
          required
        />
        <fieldset className="checks" aria-invalid={permErr ? true : undefined}>
          <legend className="field__label">{t("tokens.permissions")}</legend>
          {held.map((p) => (
            <label key={p} className="check">
              <input
                type="checkbox"
                checked={perms.includes(p)}
                onChange={(e) =>
                  setPerms((xs) => (e.target.checked ? [...xs, p] : xs.filter((x) => x !== p)))
                }
              />
              {t(`permission.${p}`)}
            </label>
          ))}
          {permErr && <span className="field__error">{permErr}</span>}
        </fieldset>
        <TextField
          label={t("tokens.days")}
          hint={t("tokens.daysHint")}
          type="number"
          inputMode="numeric"
          min={1}
          max={365}
          value={days}
          onChange={(e) => setDays(e.target.value)}
          error={errs.expires_in_days}
        />
        {create.isError && Object.keys(errs).length === 0 && <ErrorCallout error={create.error} />}
        <div>
          <Button type="submit" variant="primary" disabled={create.isPending || perms.length === 0}>
            {t("tokens.create")}
          </Button>
        </div>
      </form>

      <Dialog
        open={create.isSuccess}
        onOpenChange={(o) => !o && create.reset()}
        title={t("tokens.createdTitle")}
        description={t("tokens.createdHint")}
      >
        {create.data && (
          <div className="stack">
            <p className="mono break token-value">{create.data.token}</p>
            <CopyButton text={create.data.token} />
          </div>
        )}
      </Dialog>
      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => !o && setRevoking(null)}
        title={t("tokens.revokeTitle", { name: revoking?.name ?? "" })}
        description={t("tokens.revokeHint")}
        confirmLabel={t("tokens.revokeConfirm")}
        danger
        pending={revoke.isPending}
        error={revoke.error}
        onConfirm={() => revoking && revoke.mutate(revoking.id)}
      />
    </section>
  );
}
