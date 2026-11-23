import "@fontsource/barlow/latin-400.css";
import "@fontsource/barlow/latin-500.css";
import "@fontsource/barlow/latin-600.css";
import "@fontsource/barlow-semi-condensed/latin-600.css";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/components.css";
import "@shared/status-band.css";
import "@shared/diff.css";
import "./styles/library.css";
import "./styles/tune.css";
import "./styles/item.css";
import "./styles/m4.css";

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { createQueryClient } from "./api/queryClient";
import { App } from "./App";
import { initI18n } from "./i18n";
import { applyTheme, readTheme } from "./theme/theme";

applyTheme(readTheme());
initI18n();

const root = document.getElementById("root");
if (!root) throw new Error("#root 不存在");
// PWA（ENTRY-R3）：service worker 只為了可以安裝成 App，不快取任何回應。開發模式與測試環境不註冊。
if (import.meta.env.PROD && "serviceWorker" in navigator) {
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch(() => {
      // 註冊失敗只影響安裝，不影響使用
    });
  });
}

createRoot(root).render(
  <StrictMode>
    <App queryClient={createQueryClient()} />
  </StrictMode>,
);
