// 輸出（pages.md §12 與 2026-11-23 的修訂、VAULT 待決問題 V1、V18）：本機影片旁（sidecar）與指定資料夾（folder）
// 在處理方案的 outputs 中設定；全域設定沒有 outputs 區段（/api/settings 不接受），所以這裡只說明。
// vault 與 hook 只能在設定檔定義：唯讀列出 GET /api/vaults（id、adapter、base）與 GET /api/hooks（只有 id），
// 沒有編輯的入口；處理方案的編輯器可以選用它們。
import { useTranslation } from "react-i18next";

import { useHooks, useVaults } from "../../api/outputCatalog";
import { ErrorCallout, Spinner } from "../../components";
import { Panel } from "./shared";

export function OutputsSection() {
  const { t } = useTranslation();
  const vaults = useVaults();
  const hooks = useHooks();
  const vaultList = vaults.data?.vaults ?? [];
  const hookList = hooks.data?.hooks ?? [];
  return (
    <>
      <Panel title={t("settings.outputs.localTitle")}>
        <p className="settings-panel__description">{t("settings.outputs.localBody")}</p>
        <dl className="settings-dl">
          <dt>{t("settings.outputs.sidecar")}</dt>
          <dd>{t("settings.outputs.sidecarBody")}</dd>
          <dt>{t("settings.outputs.folder")}</dt>
          <dd>{t("settings.outputs.folderBody")}</dd>
        </dl>
      </Panel>
      <Panel title={t("settings.outputs.vaultTitle")}>
        <p className="settings-panel__description">{t("settings.outputs.vaultBody")}</p>
        {vaults.isPending && <Spinner label={t("common.loading")} showLabel />}
        {vaults.error != null && (
          <ErrorCallout
            error={vaults.error}
            actions={[{ label: t("common.retry"), onClick: () => void vaults.refetch() }]}
          />
        )}
        {vaults.data && vaultList.length === 0 && (
          <p className="secondary">{t("settings.outputs.noVaults")}</p>
        )}
        {vaultList.length > 0 && (
          <ul className="settings-rows" aria-label={t("settings.outputs.vaultTitle")}>
            {vaultList.map((v) => (
              <li key={v.id} className="settings-row">
                <div className="settings-row__main">
                  <code className="settings-row__name">{v.id}</code>
                  <span>
                    {t(`settings.outputs.adapter.${v.adapter}`, { defaultValue: v.adapter })}
                  </span>
                  <span className="settings-row__meta">
                    {t("settings.outputs.base", { base: v.base })}
                  </span>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Panel>
      <Panel title={t("settings.outputs.hookTitle")}>
        <p className="settings-panel__description">{t("settings.outputs.hookBody")}</p>
        {hooks.isPending && <Spinner label={t("common.loading")} showLabel />}
        {hooks.error != null && (
          <ErrorCallout
            error={hooks.error}
            actions={[{ label: t("common.retry"), onClick: () => void hooks.refetch() }]}
          />
        )}
        {hooks.data && hookList.length === 0 && (
          <p className="secondary">{t("settings.outputs.noHooks")}</p>
        )}
        {hookList.length > 0 && (
          <ul className="settings-rows" aria-label={t("settings.outputs.hookTitle")}>
            {hookList.map((h) => (
              <li key={h.id} className="settings-row">
                <div className="settings-row__main">
                  <code className="settings-row__name">{h.id}</code>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Panel>
    </>
  );
}
