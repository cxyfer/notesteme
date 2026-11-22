import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach } from "vitest";

import i18next from "i18next";

import { initI18n } from "../i18n";

initI18n("zh-Hant");

// jsdom 沒有實作 scrollTo（TanStack Router 的捲動還原會呼叫它）。
window.scrollTo = () => {};
// jsdom 沒有 pointer capture（Radix Toast 的滑動關閉會呼叫）。
Element.prototype.hasPointerCapture ??= () => false;
Element.prototype.releasePointerCapture ??= () => {};

// jsdom 沒有 EventSource：用什麼都不做的替身，個別測試需要時再換成 FakeEventSource。
class SilentEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  readyState = 0;
  onopen: ((e: Event) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  constructor(public url: string) {}
  addEventListener() {}
  removeEventListener() {}
  close() {
    this.readyState = 2;
  }
}
if (!("EventSource" in globalThis)) {
  (globalThis as unknown as { EventSource: unknown }).EventSource = SilentEventSource;
}

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
});
