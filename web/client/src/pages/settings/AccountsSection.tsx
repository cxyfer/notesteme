// 帳號與 Cookie（pages.md §12、SEC-R4、SEC-R12）：上傳 YouTube、Bilibili cookie（加密存放，每個平台一份），
// 並顯示每個平台的狀態（GET /api/cookies）：上傳時間、最近一次成功使用、是否失效。
//
// 安全：cookie 內容只在這個元件的 state，送出後立即清空；選擇檔案時內容不顯示在畫面上。
// 狀態 API 只回時間與狀態，永遠不含 cookie 的值。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Fragment, useRef, useState, type FormEvent, type RefObject } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, ErrorCallout, useToast } from "../../components";
import { formatDateTime, formatRelativeTime } from "../../i18n";
import { Loading, Panel } from "./shared";

const PLATFORMS = ["youtube", "bilibili"] as const;
type Platform = (typeof PLATFORMS)[number];

type CookieStatus = components["schemas"]["CookieStatusBody"];

interface Uploaded {
  cookies: number;
  updated_at: string;
}

export function AccountsSection() {
  const { t } = useTranslation();
  const query = useQuery({ queryKey: queryKeys.cookies, queryFn: () => api.get("/api/cookies") });
  const byPlatform = new Map((query.data?.cookies ?? []).map((c) => [c.platform, c]));
  return (
    <>
      <p className="settings-panel__description">{t("settings.accounts.intro")}</p>
      {query.isError && (
        <ErrorCallout
          error={query.error}
          message={t("settings.accounts.statusError")}
          actions={[{ label: t("common.retry"), onClick: () => void query.refetch() }]}
        />
      )}
      {PLATFORMS.map((p) => (
        <CookieForm
          key={p}
          platform={p}
          status={byPlatform.get(p)}
          statusPending={query.isPending}
        />
      ))}
    </>
  );
}

/** 一個平台的狀態：狀態文字、上傳時間、最近一次成功使用；失效時另外提示並提供「重新上傳」。 */
function CookieStatusView({
  status,
  name,
  focusTarget,
}: {
  status: CookieStatus;
  name: string;
  focusTarget: RefObject<HTMLTextAreaElement | null>;
}) {
  const { t } = useTranslation();
  const rows: [string, string | undefined][] = [
    ["uploaded", status.updated_at],
    ["lastOk", status.last_ok_at],
    ["lastUsed", status.last_used_at],
  ];
  return (
    <>
      <p className="settings-note" data-kind={status.state === "ok" ? "ok" : undefined}>
        {t(`settings.accounts.state.${status.state}`, { name })}
      </p>
      {status.state !== "missing" && (
        <dl className="settings-dl">
          {rows.map(([key, at]) => (
            <Fragment key={key}>
              <dt>{t(`settings.accounts.when.${key}`)}</dt>
              <dd>
                {at ? (
                  <time dateTime={at} title={formatDateTime(at)}>
                    {formatRelativeTime(at)}
                  </time>
                ) : (
                  t("settings.accounts.never")
                )}
              </dd>
            </Fragment>
          ))}
        </dl>
      )}
      {status.state === "expired" && (
        <ErrorCallout
          error={undefined}
          live={false}
          category="needs_action"
          message={t("settings.accounts.expired", {
            name,
            time: status.expired_at ? formatDateTime(status.expired_at) : "",
          })}
          actions={[
            {
              label: t("settings.accounts.reupload", { name }),
              primary: true,
              onClick: () => focusTarget.current?.focus(),
            },
          ]}
        />
      )}
    </>
  );
}

function CookieForm({
  platform,
  status,
  statusPending,
}: {
  platform: Platform;
  status?: CookieStatus;
  statusPending: boolean;
}) {
  const { t } = useTranslation();
  const toast = useToast();
  const qc = useQueryClient();
  const textArea = useRef<HTMLTextAreaElement>(null);
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
      // 重新上傳會清掉失效的標記（SEC-R12）：重新讀取狀態。
      void qc.invalidateQueries({ queryKey: queryKeys.cookies });
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  return (
    <Panel title={t("settings.accounts.title", { name })}>
      {statusPending ? (
        <Loading />
      ) : (
        status && <CookieStatusView status={status} name={name} focusTarget={textArea} />
      )}
      <form className="settings-form" onSubmit={onSubmit} noValidate>
        <div className="field">
          <label className="field__label" htmlFor={textId}>
            {t("settings.accounts.paste")}
          </label>
          <textarea
            ref={textArea}
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
