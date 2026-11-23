// 匯入 bundle（BND-R3、R5、R9、R10）：設定「系統」分區的一個區塊，由 SystemSection lazy 載入。
//
//   - multipart 的 file；勾選「已存在時加成新版本」送 on_existing=add_versions。
//   - 上傳上限只在設定檔（bundles.import.max_upload_bytes，預設 64 MiB），API 不提供：前端只提示，不擋，以 413 的訊息為準。
//   - 422 顯示伺服器的訊息、出錯的項目（entry）與 manifest 的逐欄錯誤；409 已存在時提示勾選「加成新版本」。
//   - 匯入後不會自動處理（BND-R8），成功時連到匯入的影音。
import { Link } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, ErrorCallout } from "../../components";
import { formatBytes } from "../../i18n";
import { Panel, SaveError } from "./shared";

type ImportResult = components["schemas"]["BundleImportResult"];

const EXISTS = "bundle_item_exists";

export function BundleImport() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const id = useId();
  const fileRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File>();
  const [addVersions, setAddVersions] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const [result, setResult] = useState<ImportResult>();

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!file) return;
    setPending(true);
    setError(undefined);
    setResult(undefined);
    const form = new FormData();
    form.append("file", file);
    try {
      const out = await api.post("/api/bundles/import", {
        body: form,
        query: addVersions ? { on_existing: "add_versions" } : undefined,
      });
      setResult(out);
      setFile(undefined);
      if (fileRef.current) fileRef.current.value = "";
      await qc.invalidateQueries({ queryKey: queryKeys.items.all });
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  const apiErr = isApiError(error) ? error : undefined;
  return (
    <Panel title={t("settings.bundle.title")} description={t("settings.bundle.hint")}>
      <form className="settings-form" onSubmit={onSubmit} noValidate>
        <div className="field">
          <label className="field__label" htmlFor={id}>
            {t("settings.bundle.file")}
          </label>
          <input
            ref={fileRef}
            id={id}
            className="field__input"
            type="file"
            accept=".zip,application/zip"
            aria-describedby={`${id}-hint`}
            onChange={(e) => {
              setFile(e.target.files?.[0] ?? undefined);
              setResult(undefined);
              setError(undefined);
            }}
          />
          <span id={`${id}-hint`} className="field__hint">
            {file
              ? t("settings.bundle.selected", { name: file.name, size: formatBytes(file.size) })
              : t("settings.bundle.limit")}
          </span>
        </div>
        <label className="check-row">
          <input
            type="checkbox"
            checked={addVersions}
            onChange={(e) => setAddVersions(e.target.checked)}
          />
          {t("settings.bundle.addVersions")}
        </label>
        <div className="settings-actions">
          <Button type="submit" variant="primary" disabled={!file || pending}>
            {pending ? t("settings.bundle.importing") : t("settings.bundle.import")}
          </Button>
        </div>
      </form>
      {result && (
        <p className="settings-note" data-kind="ok" role="status">
          {t("settings.bundle.done", { imported: result.imported, skipped: result.skipped })}{" "}
          <Link to="/items/$id" params={{ id: result.item.id }}>
            {result.item.title || result.item.ref}
          </Link>
        </p>
      )}
      {apiErr?.code === EXISTS ? (
        <ErrorCallout
          error={error}
          message={t("settings.bundle.exists")}
          actions={
            addVersions
              ? []
              : [{ label: t("settings.bundle.addVersions"), onClick: () => setAddVersions(true) }]
          }
        />
      ) : apiErr?.body.errors?.length ? (
        <SaveError error={error} />
      ) : (
        error !== undefined && <ErrorCallout error={error} />
      )}
      {apiErr?.body.entry && (
        <p className="settings-note">
          {t("settings.bundle.entry")} <code>{apiErr.body.entry}</code>
        </p>
      )}
    </Panel>
  );
}
