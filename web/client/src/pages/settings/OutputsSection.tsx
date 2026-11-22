// 輸出（pages.md §12 與 2026-11-23 的修訂）：本機影片旁（sidecar）與指定資料夾（folder）在處理方案的 outputs 中設定；
// 全域設定沒有 outputs 區段（/api/settings 不接受），所以這裡只說明。vault 與 hook 只能在設定檔定義，M3 前尚未啟用。
import { useTranslation } from "react-i18next";

import { NotAvailable, Panel } from "./shared";

export function OutputsSection() {
  const { t } = useTranslation();
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
        <NotAvailable>{t("settings.outputs.notEnabled")}</NotAvailable>
      </Panel>
      <Panel title={t("settings.outputs.hookTitle")}>
        <p className="settings-panel__description">{t("settings.outputs.hookBody")}</p>
        <NotAvailable>{t("settings.outputs.notEnabled")}</NotAvailable>
      </Panel>
    </>
  );
}
