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
createRoot(root).render(
  <StrictMode>
    <App queryClient={createQueryClient()} />
  </StrictMode>,
);
