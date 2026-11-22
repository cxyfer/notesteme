import { useSyncExternalStore } from "react";

import { currentTheme, subscribeTheme, type Theme } from "./theme";

/** 目前的主題；任何地方呼叫 saveTheme 都會讓所有使用者重新渲染。 */
export function useTheme(): Theme {
  return useSyncExternalStore(subscribeTheme, currentTheme, currentTheme);
}
