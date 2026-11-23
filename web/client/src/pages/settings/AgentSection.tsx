// MCP 與 Agent（pages.md §12、AUTH-R7～R10、AUTH-R22、AGT A3）：
//   - token：列表、建立（scope、到期時間）、撤銷與輪替（都先確認，AUTH-R21、R22）。
//   - Claude Code 的設定片段。
//   - Agent：一次最多領取幾個、租約時長、逾時後的處理（/api/settings/agent，整段取代）。
//
// 安全：建立後的明文 token 只放在這個元件的 state，關閉對話框時清掉；不用 useMutation 持有它
// （data 會留在 MutationCache），不放進 toast、錯誤訊息、URL 或 localStorage。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ChangeEvent, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import { Button, Dialog, ErrorCallout, TextField, useToast } from "../../components";
import { formatDateTime } from "../../i18n";
import { Loading, Panel, SaveError, useSettingsSection } from "./shared";

const SCOPES = ["read", "agent", "ingest", "admin"] as const;
// AUTH-R22：7 天、30 天、90 天（預設）、1 年、不設到期；值是 expires_in（空字串代表省略）
const EXPIRES = ["7d", "30d", "90d", "365d", ""] as const;

interface TokenInfo {
  id: string;
  name: string;
  prefix: string;
  scopes: string[] | null;
  created_at: string;
  expires_at?: string;
  last_used_at?: string;
  revoked_at?: string;
}

type TokenState = "active" | "expired" | "revoked";

function tokenState(tk: TokenInfo, now = Date.now()): TokenState {
  if (tk.revoked_at) return "revoked";
  if (tk.expires_at && Date.parse(tk.expires_at) <= now) return "expired";
  return "active";
}

/** Claude Code 加入 MCP server 的指令；token 預設是占位字串。 */
export function claudeCodeSnippet(token = "<token>", origin = window.location.origin) {
  return `claude mcp add --transport http notesteme ${origin}/mcp --header "Authorization: Bearer ${token}"`;
}

export function AgentSection() {
  const { t } = useTranslation();
  return (
    <>
      <Tokens />
      <Panel title={t("settings.agent.snippetTitle")} description={t("settings.agent.snippetHint")}>
        <pre className="settings-code">
          <code>{claudeCodeSnippet()}</code>
        </pre>
      </Panel>
      <AgentSettings />
    </>
  );
}

function Tokens() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const query = useQuery({
    queryKey: queryKeys.tokens,
    queryFn: () => api.get("/api/tokens"),
  });
  const [revoking, setRevoking] = useState<TokenInfo>();
  const [revokeError, setRevokeError] = useState<unknown>();
  const [rotating, setRotating] = useState<TokenInfo>();
  // 輪替後的明文 token：只在這裡，關閉對話框時清掉
  const [rotated, setRotated] = useState<{ name: string; token: string }>();

  async function rotate(tk: TokenInfo) {
    setRotating(undefined);
    setRevokeError(undefined);
    try {
      const out = await api.post<"/api/tokens/{id}/rotate">(
        apiPath("/api/tokens/{id}/rotate", { id: tk.id }),
      );
      setRotated({ name: out.name, token: out.token });
      await qc.invalidateQueries({ queryKey: queryKeys.tokens });
    } catch (err) {
      setRevokeError(err);
    }
  }

  async function revoke(tk: TokenInfo) {
    setRevoking(undefined);
    setRevokeError(undefined);
    try {
      await api.delete<"/api/tokens/{id}">(apiPath("/api/tokens/{id}", { id: tk.id }));
      await qc.invalidateQueries({ queryKey: queryKeys.tokens });
      toast.show({ title: t("settings.agent.revoked", { name: tk.name }) });
    } catch (err) {
      setRevokeError(err);
    }
  }

  const tokens = (query.data?.tokens ?? []) as TokenInfo[];
  return (
    <Panel title={t("settings.agent.tokensTitle")} description={t("settings.agent.tokensHint")}>
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorCallout
          error={query.error}
          actions={[{ label: t("common.retry"), onClick: () => void query.refetch() }]}
        />
      ) : tokens.length === 0 ? (
        <p className="settings-panel__description">{t("settings.agent.noTokens")}</p>
      ) : (
        <div className="settings-table-wrap">
          <table className="settings-table">
            <thead>
              <tr>
                <th scope="col">{t("settings.agent.col.name")}</th>
                <th scope="col">{t("settings.agent.col.scopes")}</th>
                <th scope="col">{t("settings.agent.col.created")}</th>
                <th scope="col">{t("settings.agent.col.lastUsed")}</th>
                <th scope="col">{t("settings.agent.col.expires")}</th>
                <th scope="col">{t("settings.agent.col.state")}</th>
                <th scope="col">
                  <span className="visually-hidden">{t("settings.agent.col.actions")}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {tokens.map((tk) => {
                const st = tokenState(tk);
                return (
                  <tr key={tk.id}>
                    <td>
                      {tk.name}
                      <span className="settings-row__meta num"> {tk.prefix}…</span>
                    </td>
                    <td>{(tk.scopes ?? []).join(t("common.listSeparator"))}</td>
                    <td className="num">{formatDateTime(tk.created_at)}</td>
                    <td className="num">
                      {tk.last_used_at
                        ? formatDateTime(tk.last_used_at)
                        : t("settings.agent.never")}
                    </td>
                    <td className="num">
                      {tk.expires_at ? formatDateTime(tk.expires_at) : t("settings.agent.noExpiry")}
                    </td>
                    <td>{t(`settings.agent.state.${st}`)}</td>
                    <td>
                      {st === "active" && (
                        <>
                          <Button
                            variant="text"
                            aria-label={t("settings.agent.rotateNamed", { name: tk.name })}
                            onClick={() => setRotating(tk)}
                          >
                            {t("settings.agent.rotate")}
                          </Button>
                          <Button
                            variant="text"
                            aria-label={t("settings.agent.revokeNamed", { name: tk.name })}
                            onClick={() => setRevoking(tk)}
                          >
                            {t("settings.agent.revoke")}
                          </Button>
                        </>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {revokeError !== undefined && <ErrorCallout error={revokeError} />}
      <CreateToken />
      <Dialog
        open={revoking !== undefined}
        onOpenChange={(o) => !o && setRevoking(undefined)}
        title={t("settings.agent.revokeTitle", { name: revoking?.name ?? "" })}
        description={t("settings.agent.revokeBody")}
        closable={false}
        footer={
          <>
            <Button variant="text" onClick={() => setRevoking(undefined)}>
              {t("settings.common.cancel")}
            </Button>
            <Button variant="danger" onClick={() => revoking && void revoke(revoking)}>
              {t("settings.agent.revoke")}
            </Button>
          </>
        }
      />
      <Dialog
        open={rotating !== undefined}
        onOpenChange={(o) => !o && setRotating(undefined)}
        title={t("settings.agent.rotateTitle", { name: rotating?.name ?? "" })}
        description={t("settings.agent.rotateBody")}
        closable={false}
        footer={
          <>
            <Button variant="text" onClick={() => setRotating(undefined)}>
              {t("settings.common.cancel")}
            </Button>
            <Button variant="danger" onClick={() => rotating && void rotate(rotating)}>
              {t("settings.agent.rotate")}
            </Button>
          </>
        }
      />
      <ShownToken
        shown={rotated}
        title={t("settings.agent.rotatedTitle", { name: rotated?.name ?? "" })}
        onClose={() => setRotated(undefined)}
      />
    </Panel>
  );
}

/** 只顯示一次的明文 token（建立或輪替之後）；關閉時由呼叫端清掉 state。 */
function ShownToken({
  shown,
  title,
  onClose,
}: {
  shown?: { name: string; token: string };
  title: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState<"token" | "snippet">();
  async function copy(what: "token" | "snippet", text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(what);
    } catch {
      setCopied(undefined);
    }
  }
  return (
    <Dialog
      open={shown !== undefined}
      onOpenChange={(o) => {
        if (!o) {
          setCopied(undefined);
          onClose();
        }
      }}
      title={title}
      description={t("settings.agent.createdBody")}
    >
      {shown && (
        <div className="settings-form">
          <TextField
            label={t("settings.agent.tokenLabel")}
            readOnly
            autoComplete="off"
            spellCheck={false}
            value={shown.token}
            onFocus={(e) => e.currentTarget.select()}
          />
          <div className="settings-actions">
            <Button onClick={() => void copy("token", shown.token)}>
              {t("settings.agent.copyToken")}
            </Button>
            <Button onClick={() => void copy("snippet", claudeCodeSnippet(shown.token))}>
              {t("settings.agent.copySnippet")}
            </Button>
            <span className="settings-row__meta" aria-live="polite">
              {copied ? t("settings.agent.copied") : ""}
            </span>
          </div>
        </div>
      )}
    </Dialog>
  );
}

function CreateToken() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>(["agent"]);
  const [expires, setExpires] = useState<(typeof EXPIRES)[number]>("90d");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  // 明文 token：只在這裡，關閉對話框時清掉
  const [created, setCreated] = useState<{ name: string; token: string }>();

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (pending || name.trim() === "" || scopes.length === 0) return;
    setPending(true);
    setError(undefined);
    try {
      const out = await api.post("/api/tokens", {
        body: {
          name: name.trim(),
          scopes,
          ...(expires ? { expires_in: expires } : {}),
        },
      });
      setCreated({ name: out.name, token: out.token });
      setName("");
      await qc.invalidateQueries({ queryKey: queryKeys.tokens });
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  return (
    <>
      <form className="settings-form" onSubmit={onSubmit} noValidate>
        <h4 className="settings-subtitle">{t("settings.agent.createTitle")}</h4>
        <TextField
          label={t("settings.agent.name")}
          hint={t("settings.agent.nameHint")}
          autoComplete="off"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <fieldset className="settings-fieldset">
          <legend className="field__label">{t("settings.agent.scopes")}</legend>
          {SCOPES.map((s) => (
            <label key={s} className="settings-check">
              <input
                type="checkbox"
                checked={scopes.includes(s)}
                onChange={(e) =>
                  setScopes((xs) => (e.target.checked ? [...xs, s] : xs.filter((x) => x !== s)))
                }
              />
              <span>
                <code>{s}</code> {t(`settings.agent.scope.${s}`)}
              </span>
            </label>
          ))}
        </fieldset>
        <label className="settings-field">
          <span className="field__label">{t("settings.agent.expires")}</span>
          <select
            value={expires}
            onChange={(e) => setExpires(e.target.value as (typeof EXPIRES)[number])}
          >
            {EXPIRES.map((x) => (
              <option key={x || "never"} value={x}>
                {t(`settings.agent.expiry.${x || "never"}`)}
              </option>
            ))}
          </select>
        </label>
        {error !== undefined && <ErrorCallout error={error} />}
        <div className="settings-actions">
          <Button
            type="submit"
            variant="primary"
            disabled={pending || name.trim() === "" || scopes.length === 0}
          >
            {t("settings.agent.create")}
          </Button>
        </div>
      </form>
      <ShownToken
        shown={created}
        title={t("settings.agent.createdTitle", { name: created?.name ?? "" })}
        onClose={() => setCreated(undefined)}
      />
    </>
  );
}

const ON_TIMEOUT = ["fallback_api", "notify"] as const;
const ON_VALIDATION = ["requeue", "fallback_api"] as const;

interface AgentForm {
  max_claims: string;
  lease: string;
  timeout: string;
  on_timeout: string;
  on_validation_failure: string;
}

function toAgentForm(v: Record<string, unknown>): AgentForm {
  const s = (x: unknown) => (typeof x === "string" || typeof x === "number" ? String(x) : "");
  return {
    max_claims: s(v.max_claims),
    lease: s(v.lease),
    timeout: s(v.timeout),
    on_timeout: s(v.on_timeout),
    on_validation_failure: s(v.on_validation_failure),
  };
}

function AgentSettings() {
  const { t } = useTranslation();
  const { query } = useSettingsSection("agent");
  return (
    <Panel title={t("settings.agent.agentTitle")} description={t("settings.agent.agentHint")}>
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorCallout error={query.error} />
      ) : (
        <AgentEditor initial={toAgentForm(query.data)} />
      )}
    </Panel>
  );
}

function AgentEditor({ initial }: { initial: AgentForm }) {
  const { t } = useTranslation();
  const toast = useToast();
  const { save } = useSettingsSection("agent");
  const [form, setForm] = useState(initial);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const set = (k: keyof AgentForm, v: string) => setForm((f) => ({ ...f, [k]: v }));
  const bind = (k: keyof AgentForm) => (e: ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    set(k, e.target.value);
  const onField = {
    max_claims: bind("max_claims"),
    lease: bind("lease"),
    timeout: bind("timeout"),
    on_timeout: bind("on_timeout"),
    on_validation_failure: bind("on_validation_failure"),
  };

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setPending(true);
    setError(undefined);
    // 沒有填的欄位不送（用內建預設）；整段取代，不送空字串或 null
    const body: Record<string, unknown> = {};
    const n = form.max_claims.trim();
    if (n !== "") body.max_claims = /^\d+$/.test(n) ? Number(n) : n;
    for (const k of ["lease", "timeout", "on_timeout", "on_validation_failure"] as const) {
      const v = form[k].trim();
      if (v !== "") body[k] = v;
    }
    try {
      const out = await save(body);
      setForm(toAgentForm(out));
      toast.show({ title: t("settings.common.saved") });
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  return (
    <form className="settings-form settings-form--narrow" onSubmit={onSubmit} noValidate>
      <TextField
        label={t("settings.agent.maxClaims")}
        hint={t("settings.agent.defaultIs", { value: "4" })}
        inputMode="numeric"
        value={form.max_claims}
        onChange={onField.max_claims}
      />
      <TextField
        label={t("settings.agent.lease")}
        hint={t("settings.agent.durationHint", { value: "10m" })}
        value={form.lease}
        onChange={onField.lease}
      />
      <TextField
        label={t("settings.agent.timeout")}
        hint={t("settings.agent.durationHint", { value: "24h" })}
        value={form.timeout}
        onChange={onField.timeout}
      />
      <label className="settings-field">
        <span className="field__label">{t("settings.agent.onTimeout")}</span>
        <select value={form.on_timeout} onChange={onField.on_timeout}>
          <option value="">{t("settings.agent.useDefault")}</option>
          {ON_TIMEOUT.map((x) => (
            <option key={x} value={x}>
              {t(`settings.agent.onTimeoutOption.${x}`)}
            </option>
          ))}
        </select>
      </label>
      <label className="settings-field">
        <span className="field__label">{t("settings.agent.onValidation")}</span>
        <select value={form.on_validation_failure} onChange={onField.on_validation_failure}>
          <option value="">{t("settings.agent.useDefault")}</option>
          {ON_VALIDATION.map((x) => (
            <option key={x} value={x}>
              {t(`settings.agent.onValidationOption.${x}`)}
            </option>
          ))}
        </select>
      </label>
      {error !== undefined && <SaveError error={error} />}
      <div className="settings-actions">
        <Button type="submit" disabled={pending}>
          {t("settings.common.save")}
        </Button>
      </div>
    </form>
  );
}
