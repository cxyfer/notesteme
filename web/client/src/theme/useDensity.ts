import { useSyncExternalStore } from "react";

import { currentDensity, subscribeDensity, type Density } from "./density";

/** 目前的介面密度；呼叫 saveDensity 後所有使用者重新渲染。 */
export function useDensity(): Density {
  return useSyncExternalStore(subscribeDensity, currentDensity, currentDensity);
}
