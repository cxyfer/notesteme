import { cleanup } from "@testing-library/react";
import i18next from "i18next";
import { afterEach, beforeEach } from "vitest";

import { initI18n } from "../i18n";

initI18n("zh-Hant");

// jsdom 沒有 scrollTo、pointer capture、ResizeObserver、scrollIntoView。
window.scrollTo = () => {};
Element.prototype.hasPointerCapture ??= () => false;
Element.prototype.releasePointerCapture ??= () => {};
Element.prototype.scrollIntoView ??= () => {};
class NoopResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver ??= NoopResizeObserver;

beforeEach(async () => {
  await i18next.changeLanguage("zh-Hant");
});

afterEach(() => {
  cleanup();
  document.cookie.split(";").forEach((c) => {
    const name = c.split("=")[0]?.trim();
    if (name) document.cookie = `${name}=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/`;
  });
  localStorage.clear();
  sessionStorage.clear();
});
