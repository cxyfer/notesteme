// 服務與金鑰（pages.md §12、SEC-R4、SEC-R6、SEC-R11）：API key 儲存後只顯示末四碼，只能更換或清除；
// provider 目錄（GET /api/providers）唯讀列出內建與設定檔中的 provider、測試連線與模型的停用公告。
//
// 安全：key 只存在這個元件的 state，送出後立即清空；不用 useMutation（它的 variables 會留在 MutationCache），
// 也不寫進 URL、localStorage、log 或 toast。PUT services 是部分更新：每次只送正在改的那一個 provider。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { describeError } from "../../api/describeError";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, Dialog, ErrorCallout, TextField, useToast } from "../../components";
import { Loading, Panel, SaveError, useSettingsSection } from "./shared";

type ProviderInfo = components["schemas"]["ProviderInfo"];
type ProviderTestResult = components["schemas"]["ProviderTestResult"];

// 可以設定 API key 的服務商是後端的白名單（internal/secrets/services.go 的 allowedServiceProviders），
// 不是 provider 目錄（GET /api/providers 列的是 provider 本身，id 可以是設定檔中的任意名稱），所以這裡照抄。
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
      </Panel>
      <ProvidersPanel />
      <SupadataMode />
    </>
  );
}

// provider 目錄（SEC-R11）：全部唯讀；command 只能在設定檔中定義。id 來自設定檔時是任意字串，直接顯示原文。
function ProvidersPanel() {
  const { t } = useTranslation();
  const query = useQuery({
    queryKey: queryKeys.providers,
    queryFn: () => api.get<"/api/providers">("/api/providers"),
  });
  const providers = query.data?.providers ?? [];
  return (
    <Panel
      title={t("settings.services.providersTitle")}
      description={t("settings.services.providersHint")}
    >
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorCallout
          error={query.error}
          actions={[{ label: t("common.retry"), onClick: () => void query.refetch() }]}
        />
      ) : providers.length === 0 ? (
        <p className="settings-panel__description">{t("settings.services.providersEmpty")}</p>
      ) : (
        <ul className="settings-rows">
          {providers.map((p) => (
            <ProviderCatalogRow key={`${p.kind}/${p.id}`} provider={p} />
          ))}
        </ul>
      )}
      <p className="settings-panel__description">{t("settings.services.commandHint")}</p>
    </Panel>
  );
}

function ProviderCatalogRow({ provider: p }: { provider: ProviderInfo }) {
  const { t } = useTranslation();
  const [pending, setPending] = useState(false);
  const [result, setResult] = useState<ProviderTestResult | null>(null);
  const [error, setError] = useState<unknown>();
  const isCommand = p.type === "command";
  const deprecations = p.deprecations ?? [];

  async function runTest() {
    setPending(true);
    setResult(null);
    setError(undefined);
    try {
      setResult(
        await api.post<"/api/providers/{id}/test">(
          apiPath("/api/providers/{id}/test", { id: p.id }),
          { query: { kind: p.kind } },
        ),
      );
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  const errorMessage = error === undefined ? undefined : testErrorMessage(t, error);
  return (
    <li className="settings-row">
      <div className="settings-row__main">
        <span className="settings-row__name">{p.id}</span>
        <span className="settings-row__meta">
          {[
            t(`settings.services.kind.${p.kind}`),
            p.type,
            t(`settings.services.definedIn.${p.defined_in}`),
          ].join(" · ")}
        </span>
        {isCommand && (
          <span className="settings-row__meta">{t("settings.services.commandReadOnly")}</span>
        )}
        {deprecations.map((d) => (
          <span key={d.model} className="settings-note" data-kind="warn">
            {t("settings.services.deprecation", { model: d.model, after: d.after })}
            {d.replacement &&
              ` ${t("settings.services.deprecationReplacement", { replacement: d.replacement })}`}
            {d.message && ` ${d.message}`}
          </span>
        ))}
        {!p.testable && (
          <span className="settings-row__meta">{t("settings.services.untestable")}</span>
        )}
        {result && (
          <span className="settings-note" data-kind="ok" role="status">
            {isCommand
              ? t("settings.services.testOkCommand")
              : t("settings.services.testOk", { ms: result.latency_ms })}
          </span>
        )}
        {error !== undefined && <ErrorCallout error={error} message={errorMessage} />}
      </div>
      <div className="settings-row__actions">
        <Button
          onClick={() => void runTest()}
          disabled={pending || !p.testable}
          aria-label={t("settings.services.testLabel", { name: p.id })}
        >
          {pending ? t("settings.services.testing") : t("settings.services.test")}
        </Button>
      </div>
    </li>
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

// 測試連線的錯誤文字：upstream_failed 用後端的訊息（前端字典的 errors.upstream_failed 是講 yt-dlp 的，
// 這裡指的是 provider，後端的訊息有 provider 與狀態碼）；needs_action 的 actions 是後端的文字，接在訊息後面。
function testErrorMessage(t: TFunction, error: unknown): string {
  const base =
    isApiError(error) && error.code === "upstream_failed" && error.message
      ? error.message
      : describeError(t, error);
  const actions = isApiError(error) ? error.body.actions : undefined;
  return actions?.length
    ? `${base} ${t("settings.services.testActions", { actions: actions.join(t("common.listSeparator")) })}`
    : base;
}
