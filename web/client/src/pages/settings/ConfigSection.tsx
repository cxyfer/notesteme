// 設定匯出與匯入（SEC-R10）：匯出處理方案與預設處理方案（不含內建的 default 與任何 secrets）成 YAML；
// 匯入時後端先驗證整份，有任何錯誤就整份不套用並回 422 與逐欄位的 errors。
// 後端沒有 dry-run（SEC 規格的待決問題），所以匯入前先跳確認框說明會發生什麼，再套用。
import { useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, Dialog, ErrorCallout, useToast } from "../../components";
import { Panel, SaveError } from "./shared";

type ImportResult = components["schemas"]["ConfigImportOutputBody"];

export const EXPORT_FILENAME = "notesteme-config.yaml";

export function ConfigSection() {
  return (
    <>
      <ExportPanel />
      <ImportPanel />
    </>
  );
}

function ExportPanel() {
  const { t } = useTranslation();
  const toast = useToast();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const [profiles, setProfiles] = useState<string[]>();

  async function onExport() {
    setPending(true);
    setError(undefined);
    try {
      const out = await api.get("/api/config/export");
      const blob = new Blob([out.content], { type: "application/yaml" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = EXPORT_FILENAME;
      document.body.append(a);
      a.click();
      a.remove();
      // 部分瀏覽器在 click() 之後才開始讀 blob，立即 revoke 會讓下載失敗
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      setProfiles(out.profiles ?? []);
      toast.show({ title: t("settings.config.exported", { count: out.profiles?.length ?? 0 }) });
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  return (
    <Panel title={t("settings.config.exportTitle")} description={t("settings.config.exportHint")}>
      <div className="settings-actions">
        <Button disabled={pending} onClick={() => void onExport()}>
          {t("settings.config.export")}
        </Button>
      </div>
      {profiles && (
        <p className="settings-note" data-kind="ok">
          {profiles.length > 0
            ? t("settings.config.exportedList", { list: profiles.join(t("common.listSeparator")) })
            : t("settings.config.exportedNone")}
        </p>
      )}
      {error !== undefined && <ErrorCallout error={error} />}
    </Panel>
  );
}

function ImportPanel() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const fileInput = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<{ name: string; content: string }>();
  const [confirm, setConfirm] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const [result, setResult] = useState<ImportResult>();

  async function onImport() {
    if (!file) return;
    setConfirm(false);
    setPending(true);
    setError(undefined);
    setResult(undefined);
    try {
      const out = await api.post("/api/config/import", { body: { content: file.content } });
      setResult(out);
      setFile(undefined);
      if (fileInput.current) fileInput.current.value = "";
      await qc.invalidateQueries({ queryKey: queryKeys.profiles });
      toast.show({ title: t("settings.config.imported") });
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  const list = (xs: string[] | null | undefined) =>
    xs && xs.length > 0 ? xs.join(t("common.listSeparator")) : t("settings.config.none");

  return (
    <Panel title={t("settings.config.importTitle")} description={t("settings.config.importHint")}>
      <div className="settings-actions">
        <Button onClick={() => fileInput.current?.click()}>
          {t("settings.config.chooseFile")}
        </Button>
        <input
          ref={fileInput}
          type="file"
          accept=".yaml,.yml,application/yaml,text/yaml"
          hidden
          aria-label={t("settings.config.chooseFile")}
          onChange={(e) => {
            const f = e.target.files?.[0];
            setError(undefined);
            setResult(undefined);
            if (!f) return;
            void f.text().then((content) => setFile({ name: f.name, content }));
          }}
        />
        {file && (
          <span className="settings-row__meta">
            {t("settings.config.fileChosen", { file: file.name })}
          </span>
        )}
        <Button variant="primary" disabled={!file || pending} onClick={() => setConfirm(true)}>
          {t("settings.config.import")}
        </Button>
      </div>
      {error !== undefined && <SaveError error={error} />}
      {result && (
        <dl className="settings-dl" aria-label={t("settings.config.resultTitle")}>
          <dt>{t("settings.config.created")}</dt>
          <dd>{list(result.created)}</dd>
          <dt>{t("settings.config.updated")}</dt>
          <dd>{list(result.updated)}</dd>
          <dt>{t("settings.config.unchanged")}</dt>
          <dd>{list(result.unchanged)}</dd>
          <dt>{t("settings.config.defaultProfile")}</dt>
          <dd>
            {result.default_changed
              ? t("settings.config.defaultChanged", { name: result.default_profile ?? "" })
              : t("settings.config.defaultSame")}
          </dd>
        </dl>
      )}
      <Dialog
        open={confirm}
        onOpenChange={setConfirm}
        title={t("settings.config.confirmTitle", { file: file?.name ?? "" })}
        description={t("settings.config.confirmBody")}
        closable={false}
        footer={
          <>
            <Button variant="text" onClick={() => setConfirm(false)}>
              {t("settings.common.cancel")}
            </Button>
            <Button variant="primary" onClick={() => void onImport()}>
              {t("settings.config.import")}
            </Button>
          </>
        }
      />
    </Panel>
  );
}
