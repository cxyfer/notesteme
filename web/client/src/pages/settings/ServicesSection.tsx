// 服務與金鑰（pages.md §12、SEC-R4、SEC-R6）：API key 儲存後只顯示末四碼，只能更換或清除。
//
// 安全：key 只存在這個元件的 state，送出後立即清空；不用 useMutation（它的 variables 會留在 MutationCache），
// 也不寫進 URL、localStorage、log 或 toast。PUT services 是部分更新：每次只送正在改的那一個 provider。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import { Button, Dialog, ErrorCallout, TextField, useToast } from "../../components";
import { Loading, NotAvailable, Panel, SaveError, useSettingsSection } from "./shared";

// 後端的白名單（internal/secrets/services.go 的 allowedServiceProviders）；後端沒有列表 API，這裡照抄。
export const SERVICE_PROVIDERS = [
  "groq",
  "supadata",
  "anthropic",
  "openai",
  "gemini",
  "mistral",
  "openai-compatible",
] as const;
type ProviderId = (typeof SERVICE_PROVIDERS)[number];

interface ServiceSettings {
  providers?: Record<string, { api_key_last4?: string }>;
}

const SUPADATA_MODES = ["auto", "native", "generate"] as const;

export function ServicesSection() {
  const { t } = useTranslation();
  const query = useQuery({
    queryKey: queryKeys.settings("services"),
    queryFn: () => api.get("/api/settings/services") as Promise<ServiceSettings>,
  });
  return (
    <>
      <Panel title={t("settings.services.keysTitle")} description={t("settings.services.keysHint")}>
        {query.isPending ? (
          <Loading />
        ) : query.isError ? (
          <ErrorCallout
            error={query.error}
            actions={[{ label: t("common.retry"), onClick: () => void query.refetch() }]}
          />
        ) : (
          <ul className="settings-rows">
            {SERVICE_PROVIDERS.map((id) => (
              <ProviderRow key={id} id={id} last4={query.data.providers?.[id]?.api_key_last4} />
            ))}
          </ul>
        )}
        <NotAvailable>{t("settings.services.testUnavailable")}</NotAvailable>
      </Panel>
      <SupadataMode />
      <Panel title={t("settings.services.commandTitle")}>
        <p className="settings-panel__description">{t("settings.services.commandHint")}</p>
      </Panel>
    </>
  );
}

function ProviderRow({ id, last4 }: { id: ProviderId; last4?: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const [editing, setEditing] = useState(false);
  const [key, setKey] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const [confirmClear, setConfirmClear] = useState(false);
  const name = t(`settings.services.provider.${id}`);

  async function put(apiKey: string) {
    setPending(true);
    setError(undefined);
    try {
      const out = (await api.put<"/api/settings/{section}">("/api/settings/services", {
        body: { providers: { [id]: { api_key: apiKey } } },
      })) as ServiceSettings;
      qc.setQueryData(queryKeys.settings("services"), out);
      return true;
    } catch (err) {
      setError(err);
      return false;
    } finally {
      setPending(false);
    }
  }

  async function onSave(e: FormEvent) {
    e.preventDefault();
    const value = key.trim();
    if (!value || pending) return;
    // 送出前就清空輸入框：失敗時要重新貼上，但 key 不會留在畫面上
    setKey("");
    if (await put(value)) {
      setEditing(false);
      toast.show({ title: t("settings.services.saved", { name }) });
    }
  }

  async function onClear() {
    setConfirmClear(false);
    if (await put("")) toast.show({ title: t("settings.services.cleared", { name }) });
  }

  return (
    <li className="settings-row">
      <div className="settings-row__main">
        <span className="settings-row__name">{name}</span>
        <span className="settings-row__meta">
          {last4 ? t("settings.services.keySet", { last4 }) : t("settings.services.keyNotSet")}
        </span>
      </div>
      {!editing && (
        <div className="settings-row__actions">
          <Button onClick={() => setEditing(true)}>
            {last4 ? t("settings.services.replace") : t("settings.services.set")}
          </Button>
          {last4 && (
            <Button variant="text" onClick={() => setConfirmClear(true)}>
              {t("settings.services.clear")}
            </Button>
          )}
        </div>
      )}
      {editing && (
        <form className="settings-inline-form" onSubmit={onSave}>
          <TextField
            label={t("settings.services.keyLabel", { name })}
            type="password"
            name={`api-key-${id}`}
            autoComplete="new-password"
            spellCheck={false}
            value={key}
            onChange={(e) => setKey(e.target.value)}
            hint={t("settings.services.keyFieldHint")}
          />
          <div className="settings-actions">
            <Button type="submit" variant="primary" disabled={pending || key.trim() === ""}>
              {t("settings.services.save")}
            </Button>
            <Button
              variant="text"
              onClick={() => {
                setKey("");
                setEditing(false);
                setError(undefined);
              }}
            >
              {t("settings.common.cancel")}
            </Button>
          </div>
        </form>
      )}
      {error !== undefined && <SaveError error={error} />}
      <Dialog
        open={confirmClear}
        onOpenChange={setConfirmClear}
        title={t("settings.services.clearTitle", { name })}
        description={t("settings.services.clearBody")}
        closable={false}
        footer={
          <>
            <Button variant="text" onClick={() => setConfirmClear(false)}>
              {t("settings.common.cancel")}
            </Button>
            <Button variant="danger" onClick={() => void onClear()}>
              {t("settings.services.clear")}
            </Button>
          </>
        }
      />
    </li>
  );
}

function SupadataMode() {
  const { t } = useTranslation();
  const toast = useToast();
  const { query, save } = useSettingsSection("supadata");
  const [error, setError] = useState<unknown>();
  const mode = typeof query.data?.mode === "string" ? query.data.mode : "auto";
  return (
    <Panel title={t("settings.services.supadataTitle")}>
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorCallout error={query.error} />
      ) : (
        <label className="settings-field">
          <span className="field__label">{t("settings.services.supadataMode")}</span>
          <select
            value={mode}
            onChange={async (e) => {
              setError(undefined);
              try {
                await save({ ...query.data, mode: e.target.value });
                toast.show({ title: t("settings.common.saved") });
              } catch (err) {
                setError(err);
              }
            }}
          >
            {SUPADATA_MODES.map((m) => (
              <option key={m} value={m}>
                {t(`settings.services.supadata.${m}`)}
              </option>
            ))}
          </select>
        </label>
      )}
      {error !== undefined && <SaveError error={error} />}
    </Panel>
  );
}
