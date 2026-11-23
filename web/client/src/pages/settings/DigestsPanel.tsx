// 彙整（DIG-R8～R11）：設定「通知」分區下的彙整設定，由 NotificationsSection lazy 載入。
//
//   - 列表：期間、時間與時區、範圍、送到哪些端點、上次與下次執行、啟用狀態。
//   - 新增與編輯（同一個對話框）；編輯送 PATCH（只送改過的欄位）。停用、刪除先確認。
//   - 預覽（POST /preview）：與現在執行相同，但不送出、不改變下次執行時間。
//   - 端點只能選勾了 digest 事件的 notifier（1～5 個，DIG-R8）。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, Dialog, ErrorCallout, SelectField, TextField, useToast } from "../../components";
import { formatDateTime } from "../../i18n";
import "../../styles/ui5.css";
import { Loading, Panel, SaveError } from "./shared";

type Digest = components["schemas"]["DigestInfo"];
type Preview = components["schemas"]["DigestPreview"];
type Body = components["schemas"]["DigestBody"];

const PERIODS = ["daily", "weekly"] as const;
const WEEKDAYS = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"] as const;
const SCOPES = ["subscriptions", "all"] as const;
const ID_RULE = /^[a-z0-9-]{1,40}$/;
const TIME_RULE = /^([01]\d|2[0-3]):[0-5]\d$/;
const MAX_NOTIFIERS = 5;

function browserZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

export function DigestsPanel() {
  const { t } = useTranslation();
  const digests = useQuery({
    queryKey: queryKeys.digests,
    queryFn: ({ signal }) => api.get("/api/digests", { signal }),
  });
  const notifiers = useQuery({
    queryKey: queryKeys.notifiers,
    queryFn: ({ signal }) => api.get("/api/notifiers", { signal }),
  });
  const [editing, setEditing] = useState<Digest | "new" | null>(null);
  // 勾選了 digest 事件的端點才能選（DIG-R8）
  const targets = (notifiers.data?.notifiers ?? [])
    .filter((n) => (n.events ?? []).includes("digest"))
    .map((n) => n.id);
  const list = digests.data?.digests ?? [];

  return (
    <Panel title={t("settings.digest.title")} description={t("settings.digest.hint")}>
      {digests.isPending ? (
        <Loading />
      ) : digests.isError ? (
        <ErrorCallout
          error={digests.error}
          actions={[{ label: t("common.retry"), onClick: () => void digests.refetch() }]}
        />
      ) : list.length === 0 ? (
        <p className="settings-note">{t("settings.digest.empty")}</p>
      ) : (
        <ul className="settings-rows">
          {list.map((d) => (
            <DigestRow key={d.id} d={d} onEdit={() => setEditing(d)} />
          ))}
        </ul>
      )}
      {notifiers.isSuccess && targets.length === 0 && (
        <p className="settings-note">{t("settings.digest.needNotifier")}</p>
      )}
      <div className="settings-actions">
        <Button disabled={targets.length === 0} onClick={() => setEditing("new")}>
          {t("settings.digest.add")}
        </Button>
      </div>
      {editing !== null && (
        <DigestDialog
          current={editing === "new" ? undefined : editing}
          targets={targets}
          onClose={() => setEditing(null)}
        />
      )}
    </Panel>
  );
}

function useSchedule() {
  const { t } = useTranslation();
  return (d: Pick<Digest, "period" | "weekday" | "at" | "timezone">) =>
    d.period === "weekly"
      ? t("settings.digest.weeklyAt", {
          day: t(`settings.digest.weekday.${d.weekday ?? "mon"}`),
          at: d.at,
          tz: d.timezone,
        })
      : t("settings.digest.dailyAt", { at: d.at, tz: d.timezone });
}

function DigestRow({ d, onEdit }: { d: Digest; onEdit: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const schedule = useSchedule();
  const [confirm, setConfirm] = useState<"delete" | "disable" | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const [preview, setPreview] = useState<Preview>();
  const path = apiPath("/api/digests/{id}", { id: d.id });

  async function run(fn: () => Promise<unknown>, done: string) {
    setPending(true);
    setError(undefined);
    try {
      await fn();
      setConfirm(null);
      toast.show({ title: done });
      await qc.invalidateQueries({ queryKey: queryKeys.digests });
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }
  async function loadPreview() {
    setPending(true);
    setError(undefined);
    try {
      setPreview(
        await api.post<"/api/digests/{id}/preview">(
          apiPath("/api/digests/{id}/preview", { id: d.id }),
        ),
      );
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  return (
    <li className="settings-row">
      <div className="settings-row__main">
        <span className="settings-row__name">
          {d.id} · {schedule(d)}
          {!d.enabled && ` · ${t("settings.notify.disabled")}`}
        </span>
        <span className="settings-row__meta">
          {t(`settings.digest.scope.${d.scope}`, { defaultValue: d.scope })}
          {" · "}
          {t("settings.digest.sendTo", {
            list: (d.notifiers ?? []).join(t("common.listSeparator")),
          })}
        </span>
        <span className="settings-row__meta">
          {t("settings.digest.runs", {
            last: d.last_run_at ? formatDateTime(d.last_run_at) : t("settings.digest.never"),
            next: d.next_run_at ? formatDateTime(d.next_run_at) : "—",
          })}
        </span>
        {error !== undefined && <ErrorCallout error={error} />}
        {preview && (
          <div className="digest-preview">
            <p className="settings-row__meta">
              {t("settings.digest.previewCount", { count: (preview.items ?? []).length })}
            </p>
            <pre className="settings-code">{preview.text}</pre>
          </div>
        )}
      </div>
      <div className="settings-row__actions">
        <Button disabled={pending} onClick={() => void loadPreview()}>
          {t("settings.digest.preview")}
        </Button>
        <Button onClick={onEdit}>{t("settings.notify.edit")}</Button>
        {d.enabled ? (
          <Button onClick={() => setConfirm("disable")}>{t("settings.notify.disable")}</Button>
        ) : (
          <Button
            disabled={pending}
            onClick={() =>
              void run(
                () => api.patch<"/api/digests/{id}">(path, { body: { enabled: true } }),
                t("settings.notify.enabledDone", { id: d.id }),
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
            ? t("settings.digest.deleteTitle", { id: d.id })
            : t("settings.digest.disableTitle", { id: d.id })
        }
        description={
          confirm === "delete"
            ? t("settings.digest.deleteDescription")
            : t("settings.digest.disableDescription")
        }
        footer={
          <Button
            variant="danger"
            disabled={pending}
            onClick={() =>
              void (confirm === "delete"
                ? run(
                    () => api.delete<"/api/digests/{id}">(path),
                    t("settings.notify.deleted", { id: d.id }),
                  )
                : run(
                    () => api.patch<"/api/digests/{id}">(path, { body: { enabled: false } }),
                    t("settings.notify.disabledDone", { id: d.id }),
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

function DigestDialog({
  current,
  targets,
  onClose,
}: {
  current?: Digest;
  targets: string[];
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const isNew = !current;
  const [id, setId] = useState("");
  const [period, setPeriod] = useState(current?.period ?? "daily");
  const [weekday, setWeekday] = useState(current?.weekday ?? "mon");
  const [at, setAt] = useState(current?.at ?? "08:00");
  const [timezone, setTimezone] = useState(current?.timezone ?? browserZone());
  const [scope, setScope] = useState(current?.scope ?? "subscriptions");
  const [chosen, setChosen] = useState<string[]>(
    current?.notifiers ?? (targets.length === 1 ? targets : []),
  );
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  // 編輯時也列出已選但不再勾 digest 的端點，讓使用者可以取消勾選
  const options = [...new Set([...targets, ...(current?.notifiers ?? [])])];

  const badId = isNew && !ID_RULE.test(id);
  const badAt = !TIME_RULE.test(at);
  const badNotifiers = chosen.length === 0 || chosen.length > MAX_NOTIFIERS;
  const bad = badId || badAt || badNotifiers || timezone.trim() === "";

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (bad) return;
    const body: Body = {
      period,
      at,
      timezone: timezone.trim(),
      scope,
      notifiers: chosen,
      ...(period === "weekly" ? { weekday } : {}),
    };
    setPending(true);
    setError(undefined);
    try {
      if (current) {
        // 只送改過的欄位（PATCH，DIG-R8）
        const patch: Body = {};
        for (const k of Object.keys(body) as (keyof Body)[]) {
          if (JSON.stringify(body[k]) !== JSON.stringify(current[k as keyof Digest]))
            (patch as Record<string, unknown>)[k] = body[k];
        }
        await api.patch<"/api/digests/{id}">(apiPath("/api/digests/{id}", { id: current.id }), {
          body: patch,
        });
      } else {
        await api.post("/api/digests", { body: { id, ...body } });
      }
      toast.show({ title: t("common.saved") });
      await qc.invalidateQueries({ queryKey: queryKeys.digests });
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
        isNew ? t("settings.digest.addTitle") : t("settings.digest.editTitle", { id: current.id })
      }
      footer={
        <Button variant="primary" type="submit" form="digest-form" disabled={bad || pending}>
          {pending ? t("common.saving") : t("settings.common.save")}
        </Button>
      }
    >
      <form id="digest-form" className="form-stack" onSubmit={submit} noValidate>
        {isNew && (
          <TextField
            label={t("settings.notify.id")}
            hint={t("settings.digest.idHint")}
            value={id}
            autoComplete="off"
            spellCheck={false}
            error={id !== "" && badId ? t("settings.digest.idHint") : undefined}
            onChange={(e) => setId(e.target.value)}
          />
        )}
        <SelectField
          label={t("settings.digest.period")}
          value={period}
          options={PERIODS.map((p) => ({ value: p, label: t(`settings.digest.periodOf.${p}`) }))}
          onChange={(e) => setPeriod(e.target.value)}
        />
        {period === "weekly" && (
          <SelectField
            label={t("settings.digest.weekdayLabel")}
            value={weekday}
            options={WEEKDAYS.map((w) => ({ value: w, label: t(`settings.digest.weekday.${w}`) }))}
            onChange={(e) => setWeekday(e.target.value)}
          />
        )}
        <TextField
          label={t("settings.digest.at")}
          type="time"
          value={at}
          error={badAt ? t("settings.digest.atHint") : undefined}
          onChange={(e) => setAt(e.target.value)}
        />
        <TextField
          label={t("settings.digest.timezone")}
          hint={t("settings.digest.timezoneHint")}
          value={timezone}
          autoComplete="off"
          spellCheck={false}
          onChange={(e) => setTimezone(e.target.value)}
        />
        <SelectField
          label={t("settings.digest.scopeLabel")}
          value={scope}
          options={SCOPES.map((s) => ({ value: s, label: t(`settings.digest.scope.${s}`) }))}
          onChange={(e) => setScope(e.target.value)}
        />
        <fieldset className="plain-fieldset">
          <legend>{t("settings.digest.notifiers")}</legend>
          {options.map((n) => (
            <label className="check-row" key={n}>
              <input
                type="checkbox"
                checked={chosen.includes(n)}
                onChange={(e) =>
                  setChosen((prev) =>
                    e.target.checked ? [...prev, n] : prev.filter((x) => x !== n),
                  )
                }
              />
              {n}
            </label>
          ))}
          {badNotifiers && <p className="field__error">{t("settings.digest.notifiersHint")}</p>}
        </fieldset>
        {error !== undefined && <SaveError error={error} />}
      </form>
    </Dialog>
  );
}
