// 設定「Hub」分區（HSHR-R1、R9、R12，HSHR S9）：URL（只能寫在設定檔，唯讀）、Hub token、測試連線、
// ASR 前查詢的開關與說明，以及用 Hub 上的原始字幕重新生成。這個檔案由 SettingsPage lazy 載入。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, request } from "../../api/client";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, ErrorCallout, TextField, useToast } from "../../components";
import { Loading, Panel, SaveError } from "../settings/shared";
import "../../styles/hub.css";
import { parseVersionRef } from "./hub";

export function HubSection() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({
    queryKey: queryKeys.hubSettings,
    queryFn: () => api.get("/api/settings/hub"),
  });
  const [token, setToken] = useState("");
  const [ack, setAck] = useState(false);
  const [ref, setRef] = useState("");
  const save = useMutation({
    mutationFn: (body: { token?: string; precheck?: { enabled: boolean; acknowledge?: string } }) =>
      api.put("/api/settings/hub", { body }),
    onSuccess: (data, body) => {
      qc.setQueryData(queryKeys.hubSettings, data);
      setToken("");
      setAck(false);
      toast.show({
        title:
          body.token === undefined
            ? t("common.saved")
            : body.token
              ? t("hub.tokenSaved")
              : t("hub.tokenRemoved"),
      });
    },
  });
  const test = useMutation({ mutationFn: () => api.post("/api/settings/hub/test") });
  const regen = useMutation({
    // 202 不在 ResponseOf 的對應中：明確標出回應的型別
    mutationFn: (version: string) =>
      request<components["schemas"]["HubImportView"]>("POST", "/api/hub/import", {
        body: { version },
      }),
    onSuccess: () => setRef(""),
  });

  if (q.isPending) return <Loading />;
  if (q.isError) return <ErrorCallout error={q.error} />;
  const s = q.data;
  const pre = s.precheck;
  const submitToken = (e: FormEvent) => {
    e.preventDefault();
    if (token.trim()) save.mutate({ token: token.trim() });
  };
  const version = parseVersionRef(ref);

  return (
    <>
      <Panel title={t("hub.connection")} description={t("hub.connectionHint")}>
        <dl className="hub-facts">
          <dt>{t("hub.url")}</dt>
          <dd className="mono">{s.url || t("hub.noUrl")}</dd>
          <dt>{t("hub.token")}</dt>
          <dd>{s.token_set ? t("hub.tokenSet") : t("hub.tokenUnset")}</dd>
        </dl>
        <form className="hub-row" onSubmit={submitToken}>
          <TextField
            label={s.token_set ? t("hub.replaceToken") : t("hub.token")}
            type="password"
            autoComplete="off"
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
          <Button type="submit" disabled={!token.trim() || save.isPending}>
            {t("hub.saveToken")}
          </Button>
          {s.token_set && (
            <Button
              variant="text"
              disabled={save.isPending}
              onClick={() => save.mutate({ token: "" })}
            >
              {t("hub.removeToken")}
            </Button>
          )}
        </form>
        <div className="hub-row">
          <Button onClick={() => test.mutate()} disabled={!s.url || !s.token_set || test.isPending}>
            {t("hub.test")}
          </Button>
          <span role="status">
            {test.isSuccess &&
              t("hub.testOk", {
                name: test.data.display_name,
                permissions: (test.data.permissions ?? []).join(", "),
              })}
          </span>
        </div>
        {test.isError && <ErrorCallout error={test.error} />}
      </Panel>

      <Panel title={t("hub.precheck")}>
        <p className="hub-disclosure">{pre.disclosure}</p>
        {pre.enabled ? (
          <div className="hub-row">
            <span>{t("hub.precheckOn")}</span>
            <Button
              disabled={save.isPending}
              onClick={() => save.mutate({ precheck: { enabled: false } })}
            >
              {t("hub.precheckDisable")}
            </Button>
          </div>
        ) : (
          <div className="hub-row">
            <label className="settings-check">
              <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} />
              {t("hub.precheckAck")}
            </label>
            <Button
              disabled={!ack || save.isPending}
              onClick={() =>
                save.mutate({ precheck: { enabled: true, acknowledge: pre.disclosure_version } })
              }
            >
              {t("hub.precheckEnable")}
            </Button>
          </div>
        )}
      </Panel>
      {save.isError && <SaveError error={save.error} />}

      <Panel title={t("hub.regen")} description={t("hub.regenHint")}>
        <form
          className="hub-row"
          onSubmit={(e) => {
            e.preventDefault();
            if (version) regen.mutate(version);
          }}
        >
          <TextField
            label={t("hub.versionRef")}
            value={ref}
            maxLength={512}
            onChange={(e) => setRef(e.target.value)}
            error={ref && !version ? t("hub.badRef") : undefined}
          />
          <Button type="submit" disabled={!version || regen.isPending}>
            {t("hub.regenStart")}
          </Button>
        </form>
        {regen.isSuccess && (
          <p role="status">
            {t("hub.regenStarted")}{" "}
            <Link to="/items/$id" params={{ id: regen.data.item.id }}>
              {t("hub.openItem")}
            </Link>
          </p>
        )}
        {regen.isError &&
          (isApiError(regen.error) &&
          regen.error.status === 422 &&
          regen.error.body.errors?.length ? (
            <SaveError error={regen.error} />
          ) : (
            <ErrorCallout error={regen.error} />
          ))}
      </Panel>
    </>
  );
}
