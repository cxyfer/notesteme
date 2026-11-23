// 通知（pages.md §12、NOTIFY-R1～R3、R6、R12）：ntfy、Discord、Telegram、webhook 的端點，
// 分別勾選要通知的事件；傳送測試通知；最近的通知紀錄。
//
// 安全（同 UI-3b 決定 6）：webhook 網址、token、secret 等密鑰只寫不讀：API 只回末四碼與 url_host；
// 輸入的值只在元件 state，送出時立即清空，不用 useMutation，不進 URL、toast 或錯誤訊息。
// PATCH 沒有重新輸入的密鑰就省略（後端保留原值，NOTIFY-R6）。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import type { components } from "../../api/schema";
import { queryKeys } from "../../api/queryKeys";
import { Button, Dialog, ErrorCallout, SelectField, TextField, useToast } from "../../components";
import { formatDateTime } from "../../i18n";
import { Loading, Panel, SaveError } from "./shared";

type Notifier = components["schemas"]["Info"];
type TestResult = components["schemas"]["TestResult"];

export const NOTIFIER_KINDS = ["ntfy", "discord", "telegram", "webhook"] as const;
type Kind = (typeof NOTIFIER_KINDS)[number];
/** NOTIFY-R3 的事件 */
export const NOTIFY_EVENTS = [
  "item.done",
  "item.failed",
  "item.blocked",
  "budget.warning",
  "digest",
  "alert",
  "source.better_available",
] as const;

interface FieldDef {
  key: string;
  secret: boolean;
  required: boolean;
}
/** NOTIFY-R2 的欄位；priority 另外處理 */
const FIELDS: Record<Kind, FieldDef[]> = {
  ntfy: [
    { key: "server", secret: false, required: false },
    { key: "topic", secret: false, required: true },
    { key: "token", secret: true, required: false },
  ],
  discord: [{ key: "webhook_url", secret: true, required: true }],
  telegram: [
    { key: "bot_token", secret: true, required: true },
    { key: "chat_id", secret: false, required: true },
    { key: "api_base", secret: false, required: false },
  ],
  webhook: [
    { key: "url", secret: true, required: true },
    { key: "secret", secret: true, required: true },
  ],
};

const ID_RULE = /^[a-z0-9][a-z0-9-]{0,39}$/;

export function NotificationsSection() {
  const { t } = useTranslation();
  const query = useQuery({
    queryKey: queryKeys.notifiers,
    queryFn: ({ signal }) => api.get("/api/notifiers", { signal }),
  });
  const [editing, setEditing] = useState<Notifier | "new" | null>(null);
  const list = query.data?.notifiers ?? [];
  return (
    <>
      <Panel title={t("settings.notify.title")} description={t("settings.notify.hint")}>
        {query.isPending ? (
          <Loading />
        ) : query.isError ? (
          <ErrorCallout
            error={query.error}
            actions={[{ label: t("common.retry"), onClick: () => void query.refetch() }]}
          />
        ) : list.length === 0 ? (
          <p className="settings-note">{t("settings.notify.empty")}</p>
        ) : (
          <ul className="settings-rows">
            {list.map((n) => (
              <NotifierRow key={n.id} n={n} onEdit={() => setEditing(n)} />
            ))}
          </ul>
        )}
        <div className="settings-actions">
          <Button onClick={() => setEditing("new")}>{t("settings.notify.add")}</Button>
        </div>
      </Panel>
      <RecentNotifications />
      {editing !== null && (
        <NotifierDialog
          current={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      )}
    </>
  );
}

function configSummary(t: ReturnType<typeof useTranslation>["t"], n: Notifier): string {
  const c = n.config as Record<string, unknown>;
  const s = (k: string) =>
    typeof c[k] === "string" || typeof c[k] === "number" ? String(c[k]) : "";
  switch (n.kind) {
    case "ntfy":
      return `${s("server")}/${s("topic")}`;
    case "discord":
      return t("settings.notify.endsWith", { last4: s("webhook_url_last4") });
    case "telegram":
      return t("settings.notify.telegramSummary", {
        chat: s("chat_id"),
        last4: s("bot_token_last4"),
      });
    case "webhook":
      return t("settings.notify.webhookSummary", {
        host: s("url_host"),
        last4: s("url_last4"),
      });
    default:
      return "";
  }
}

function NotifierRow({ n, onEdit }: { n: Notifier; onEdit: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const [confirm, setConfirm] = useState<"delete" | "disable" | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const [test, setTest] = useState<TestResult | null>(null);
  const path = apiPath("/api/notifiers/{id}", { id: n.id });

  async function run(fn: () => Promise<unknown>, done: string) {
    setPending(true);
    setError(undefined);
    try {
      await fn();
      setConfirm(null);
      toast.show({ title: done });
      await qc.invalidateQueries({ queryKey: queryKeys.notifiers });
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }
  async function sendTest() {
    setPending(true);
    setTest(null);
    setError(undefined);
    try {
      setTest(
        await api.post<"/api/notifiers/{id}/test">(
          apiPath("/api/notifiers/{id}/test", { id: n.id }),
        ),
      );
      await qc.invalidateQueries({ queryKey: queryKeys.notifications });
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }
  const events = (n.events ?? []).map((e) => t(`settings.notify.event.${e}`, { defaultValue: e }));
  return (
    <li className="settings-row">
      <div className="settings-row__main">
        <span className="settings-row__name">
          {n.id} · {t(`settings.notify.kind.${n.kind}`, { defaultValue: n.kind })}
          {!n.enabled && ` · ${t("settings.notify.disabled")}`}
        </span>
        <span className="settings-row__meta break-anywhere">{configSummary(t, n)}</span>
        <span className="settings-row__meta">
          {t("settings.notify.events", { list: events.join(t("common.listSeparator")) })}
        </span>
        {test && (
          <span className="settings-note" data-kind={test.ok ? "ok" : undefined} role="status">
            {test.ok
              ? t("settings.notify.testOk", { status: test.status ?? "" })
              : t("settings.notify.testFailed", {
                  message: test.message ?? test.error_class ?? "",
                })}
          </span>
        )}
        {error !== undefined && <ErrorCallout error={error} />}
      </div>
      <div className="settings-row__actions">
        <Button onClick={() => void sendTest()} disabled={pending}>
          {t("settings.notify.test")}
        </Button>
        <Button onClick={onEdit}>{t("settings.notify.edit")}</Button>
        {n.enabled ? (
          <Button onClick={() => setConfirm("disable")}>{t("settings.notify.disable")}</Button>
        ) : (
          <Button
            disabled={pending}
            onClick={() =>
              void run(
                () => api.patch<"/api/notifiers/{id}">(path, { body: { enabled: true } }),
                t("settings.notify.enabledDone", { id: n.id }),
              )
            }
          >
            {t("settings.notify.enable")}
          </Button>
        )}
        <Button variant="danger" onClick={() => setConfirm("delete")}>
          {t("settings.notify.delete")}
        </Button>
      </div>
      <Dialog
        open={confirm !== null}
        onOpenChange={(o) => !o && setConfirm(null)}
        title={
          confirm === "delete"
            ? t("settings.notify.deleteTitle", { id: n.id })
            : t("settings.notify.disableTitle", { id: n.id })
        }
        description={
          confirm === "delete"
            ? t("settings.notify.deleteDescription")
            : t("settings.notify.disableDescription")
        }
        footer={
          <Button
            variant="danger"
            disabled={pending}
            onClick={() =>
              void (confirm === "delete"
                ? run(
                    () => api.delete<"/api/notifiers/{id}">(path),
                    t("settings.notify.deleted", { id: n.id }),
                  )
                : run(
                    () => api.patch<"/api/notifiers/{id}">(path, { body: { enabled: false } }),
                    t("settings.notify.disabledDone", { id: n.id }),
                  ))
            }
          >
            {confirm === "delete" ? t("settings.notify.delete") : t("settings.notify.disable")}
          </Button>
        }
      />
    </li>
  );
}

function NotifierDialog({ current, onClose }: { current?: Notifier; onClose: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const cfg = (current?.config ?? {}) as Record<string, unknown>;
  const [id, setId] = useState(current?.id ?? "");
  const [kind, setKind] = useState<Kind>((current?.kind as Kind) ?? "ntfy");
  // 非機密欄位從目前的設定帶入；密鑰一律從空白開始（只寫不讀）
  const initial = (k: string) =>
    typeof cfg[k] === "string" || typeof cfg[k] === "number" ? String(cfg[k]) : "";
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      [...FIELDS.ntfy, ...FIELDS.telegram]
        .filter((f) => !f.secret)
        .map((f) => [f.key, initial(f.key)]),
    ),
  );
  const [priority, setPriority] = useState(initial("priority") || "3");
  const [secrets, setSecrets] = useState<Record<string, string>>({});
  const [events, setEvents] = useState<string[]>(
    current?.events ?? ["item.failed", "item.blocked"],
  );
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const isNew = !current;
  const fields = FIELDS[kind];

  const missing = fields.some((f) =>
    f.secret
      ? f.required && isNew && !(secrets[f.key] ?? "").trim()
      : f.required && !(values[f.key] ?? "").trim(),
  );
  const bad = (isNew && !ID_RULE.test(id)) || missing || events.length === 0;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (bad || pending) return;
    const typed = Object.fromEntries(
      fields
        .filter((f) => f.secret && (secrets[f.key] ?? "").trim())
        .map((f) => [f.key, secrets[f.key]!.trim()]),
    );
    // 送出前就清空密鑰：失敗時要重新輸入，但不會留在畫面上
    setSecrets({});
    const config: Record<string, unknown> = { ...typed };
    for (const f of fields) {
      if (f.secret) continue;
      const v = (values[f.key] ?? "").trim();
      if (v) config[f.key] = v;
      else if (!isNew && initial(f.key)) config[f.key] = null; // 清掉選填欄位
    }
    if (kind === "ntfy") config.priority = Number(priority);
    setPending(true);
    setError(undefined);
    try {
      if (isNew) {
        await api.post("/api/notifiers", { body: { id, kind, config, events } });
      } else {
        // 只送改過的非機密欄位與重新輸入的密鑰
        const patchCfg: Record<string, unknown> = {};
        for (const [k, v] of Object.entries(config)) {
          const before = k in typed ? undefined : (cfg[k] ?? null);
          if (k in typed || String(before ?? "") !== String(v ?? "")) patchCfg[k] = v;
        }
        const sameEvents =
          events.length === (current.events ?? []).length &&
          events.every((ev) => (current.events ?? []).includes(ev));
        await api.patch<"/api/notifiers/{id}">(apiPath("/api/notifiers/{id}", { id: current.id }), {
          body: {
            ...(Object.keys(patchCfg).length ? { config: patchCfg } : {}),
            ...(sameEvents ? {} : { events: NOTIFY_EVENTS.filter((ev) => events.includes(ev)) }),
          },
        });
      }
      await qc.invalidateQueries({ queryKey: queryKeys.notifiers });
      toast.show({ title: t("settings.notify.saved", { id: isNew ? id : current.id }) });
      onClose();
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={
        isNew ? t("settings.notify.addTitle") : t("settings.notify.editTitle", { id: current.id })
      }
      description={t("settings.notify.secretHint")}
      footer={
        <Button variant="primary" type="submit" form="notifier-form" disabled={bad || pending}>
          {pending ? t("common.saving") : t("settings.common.save")}
        </Button>
      }
    >
      <form
        id="notifier-form"
        className="form-stack"
        onSubmit={submit}
        noValidate
        autoComplete="off"
      >
        {isNew && (
          <>
            <TextField
              label={t("settings.notify.id")}
              value={id}
              maxLength={40}
              onChange={(e) => setId(e.target.value)}
              error={id && !ID_RULE.test(id) ? t("settings.notify.idRule") : undefined}
            />
            <SelectField
              label={t("settings.notify.kindLabel")}
              value={kind}
              options={NOTIFIER_KINDS.map((k) => ({
                value: k,
                label: t(`settings.notify.kind.${k}`),
              }))}
              onChange={(e) => setKind(e.target.value as Kind)}
            />
          </>
        )}
        {fields.map((f) =>
          f.secret ? (
            <TextField
              key={`${kind}-${f.key}`}
              label={t(`settings.notify.field.${f.key}`)}
              type="password"
              autoComplete="new-password"
              value={secrets[f.key] ?? ""}
              onChange={(e) => setSecrets((s) => ({ ...s, [f.key]: e.target.value }))}
              hint={
                !isNew && cfg[`${f.key}_last4`]
                  ? t("settings.notify.secretKept", { last4: String(cfg[`${f.key}_last4`]) })
                  : f.required
                    ? undefined
                    : t("settings.notify.optional")
              }
            />
          ) : (
            <TextField
              key={`${kind}-${f.key}`}
              label={t(`settings.notify.field.${f.key}`)}
              value={values[f.key] ?? ""}
              onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
              hint={f.required ? undefined : t(`settings.notify.fieldHint.${f.key}`)}
            />
          ),
        )}
        {kind === "ntfy" && (
          <SelectField
            label={t("settings.notify.field.priority")}
            value={priority}
            options={["1", "2", "3", "4", "5"].map((p) => ({ value: p, label: p }))}
            onChange={(e) => setPriority(e.target.value)}
          />
        )}
        <fieldset className="plain-fieldset">
          <legend>{t("settings.notify.eventsLegend")}</legend>
          {NOTIFY_EVENTS.map((ev) => (
            <label className="check-row" key={ev}>
              <input
                type="checkbox"
                checked={events.includes(ev)}
                onChange={(e) =>
                  setEvents((prev) =>
                    e.target.checked ? [...prev, ev] : prev.filter((x) => x !== ev),
                  )
                }
              />
              {t(`settings.notify.event.${ev}`)}
            </label>
          ))}
          {events.length === 0 && <p className="field__error">{t("settings.notify.needEvent")}</p>}
        </fieldset>
        {error !== undefined && <SaveError error={error} />}
      </form>
    </Dialog>
  );
}

function RecentNotifications() {
  const { t } = useTranslation();
  const query = useQuery({
    queryKey: queryKeys.notifications,
    queryFn: ({ signal }) => api.get("/api/notifications", { signal }),
  });
  const list = query.data?.notifications ?? [];
  return (
    <Panel title={t("settings.notify.recentTitle")}>
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorCallout error={query.error} />
      ) : list.length === 0 ? (
        <p className="settings-note">{t("settings.notify.recentEmpty")}</p>
      ) : (
        <div className="settings-table-wrap">
          <table className="settings-table">
            <thead>
              <tr>
                <th scope="col">{t("settings.notify.col.at")}</th>
                <th scope="col">{t("settings.notify.col.notifier")}</th>
                <th scope="col">{t("settings.notify.col.event")}</th>
                <th scope="col">{t("settings.notify.col.state")}</th>
              </tr>
            </thead>
            <tbody>
              {list.map((n) => (
                <tr key={n.id}>
                  <td className="num">{formatDateTime(n.created_at)}</td>
                  <td>{n.notifier}</td>
                  <td>{t(`settings.notify.event.${n.event}`, { defaultValue: n.event })}</td>
                  <td>
                    {t(`settings.notify.state.${n.state}`, { defaultValue: n.state })}
                    {n.attempts > 1 && ` · ${t("settings.notify.attempts", { count: n.attempts })}`}
                    {n.last_error && ` · ${n.last_error}`}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}
