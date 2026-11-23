// 書籤小工具（ENTRY-R1）：可以拖到書籤列的連結，以及手機的入口（PWA 的分享、iOS 捷徑）。
//
// 程式碼由 UI 依目前的 location.origin 產生（不經 API，不受 Host 標頭影響）。
// React 19 會擋下 javascript: 的 href，所以在 effect 中用 setAttribute 設定；
// 在這個頁面點它時不執行（CSP 本來就不允許 inline script），只能拖到書籤列。
import { useEffect, useRef, type MouseEvent } from "react";
import { useTranslation } from "react-i18next";

import { bookmarkletCode } from "../../add/entry";
import { Panel } from "./shared";

export function BookmarkletSection() {
  const { t } = useTranslation();
  const origin = window.location.origin;
  const code = bookmarkletCode(origin);
  const link = useRef<HTMLAnchorElement>(null);
  useEffect(() => {
    link.current?.setAttribute("href", code);
  }, [code]);

  return (
    <>
      <Panel
        title={t("settings.bookmarklet.title")}
        description={t("settings.bookmarklet.description")}
      >
        <p>
          <a
            ref={link}
            className="button button--primary bookmarklet"
            draggable
            onClick={(e: MouseEvent) => e.preventDefault()}
          >
            {t("settings.bookmarklet.link")}
          </a>
        </p>
        <p className="settings-note">{t("settings.bookmarklet.codeHint")}</p>
        <details>
          <summary>{t("settings.bookmarklet.code")}</summary>
          <pre className="bookmarklet__code">
            <code>{code}</code>
          </pre>
        </details>
      </Panel>
      <Panel
        title={t("settings.bookmarklet.mobileTitle")}
        description={t("settings.bookmarklet.mobile")}
      >
        <pre className="bookmarklet__code">
          <code>{`${origin}/add?url={url}`}</code>
        </pre>
      </Panel>
    </>
  );
}
