// 帳號與 Cookie（pages.md §12、SEC-R4）：上傳 YouTube、Bilibili cookie（加密存放，每個平台一份）。
//
// 安全：cookie 內容只在這個元件的 state，送出後立即清空；選擇檔案時內容不顯示在畫面上。
// 後端沒有讀取 cookie 狀態的 API，「最後一次成功使用的時間」「是否失效」做不到，只顯示這次上傳的結果。
import { useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { Button, ErrorCallout, useToast } from "../../components";
import { formatDateTime } from "../../i18n";
import { NotAvailable, Panel } from "./shared";

const PLATFORMS = ["youtube", "bilibili"] as const;
type Platform = (typeof PLATFORMS)[number];

interface Uploaded {
  cookies: number;
  updated_at: string;
}

export function AccountsSection() {
  const { t } = useTranslation();
  return (
    <>
      <p className="settings-panel__description">{t("settings.accounts.intro")}</p>
      {PLATFORMS.map((p) => (
        <CookieForm key={p} platform={p} />
      ))}
      <NotAvailable>{t("settings.accounts.statusUnavailable")}</NotAvailable>
    </>
  );
}

function CookieForm({ platform }: { platform: Platform }) {
  const { t } = useTranslation();
  const toast = useToast();
  const [text, setText] = useState("");
  const [file, setFile] = useState<{ name: string; content: string }>();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const [result, setResult] = useState<Uploaded>();
  const fileInput = useRef<HTMLInputElement>(null);
  const name = t(`platform.${platform}`);
  const textId = `cookie-${platform}`;

  function reset() {
    setText("");
    setFile(undefined);
    if (fileInput.current) fileInput.current.value = "";
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const content = file?.content ?? text;
    if (pending || content.trim() === "") return;
    reset();
    setPending(true);
    setError(undefined);
    try {
      const out = await api.post("/api/cookies", { body: { platform, content } });
      setResult({ cookies: out.cookies, updated_at: out.updated_at });
      toast.show({ title: t("settings.accounts.saved", { name, count: out.cookies }) });
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  return (
    <Panel title={t("settings.accounts.title", { name })}>
      <form className="settings-form" onSubmit={onSubmit} noValidate>
        <div className="field">
          <label className="field__label" htmlFor={textId}>
            {t("settings.accounts.paste")}
          </label>
          <textarea
            id={textId}
            className="field__input settings-textarea"
            rows={4}
            autoComplete="off"
            spellCheck={false}
            value={text}
            disabled={file !== undefined}
            aria-describedby={`${textId}-hint`}
            onChange={(e) => setText(e.target.value)}
          />
          <span id={`${textId}-hint`} className="field__hint">
            {t("settings.accounts.formatHint")}
          </span>
        </div>
        <div className="settings-actions">
          <Button onClick={() => fileInput.current?.click()}>
            {t("settings.accounts.chooseFile")}
          </Button>
          <input
            ref={fileInput}
            type="file"
            accept=".txt,text/plain"
            hidden
            aria-label={t("settings.accounts.chooseFile")}
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (!f) return;
              void f.text().then((content) => setFile({ name: f.name, content }));
            }}
          />
          {file && (
            <span className="settings-row__meta">
              {t("settings.accounts.fileChosen", { file: file.name })}{" "}
              <Button variant="text" onClick={reset}>
                {t("settings.accounts.removeFile")}
              </Button>
            </span>
          )}
          <Button type="submit" disabled={pending || (file === undefined && text.trim() === "")}>
            {t("settings.accounts.upload", { name })}
          </Button>
        </div>
      </form>
      {result && (
        <p className="settings-note" data-kind="ok">
          {t("settings.accounts.result", {
            count: result.cookies,
            time: formatDateTime(result.updated_at),
          })}
        </p>
      )}
      {error !== undefined && <ErrorCallout error={error} />}
    </Panel>
  );
}
