package entry

// Manifest 是 PWA 的 web app manifest（ENTRY-R3）。不需要登入就能讀取，所以只有固定的內容，
// 不含任何實例或使用者的資料（ENTRY 待決問題 E5）。icon 是 web/client/public/icons/ 的 PNG。
const Manifest = `{
  "name": "Notesteme",
  "short_name": "Notesteme",
  "start_url": "/",
  "scope": "/",
  "display": "standalone",
  "background_color": "#1F2A3A",
  "theme_color": "#1F2A3A",
  "icons": [
    { "src": "/icons/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any" },
    { "src": "/icons/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any" }
  ],
  "share_target": {
    "action": "/share",
    "method": "GET",
    "params": { "title": "title", "text": "text", "url": "url" }
  }
}
`

// ServiceWorker 只為了讓 UI 可以安裝成 App（ENTRY-R3）：不快取任何回應、沒有離線功能。
// fetch 事件不呼叫 respondWith，請求一律照瀏覽器原本的方式送出（/api/*、/mcp、/add、/share 也一樣）。
const ServiceWorker = `// Notesteme 的 service worker：只為了可以安裝成 App，不快取任何回應（ENTRY-R3）。
self.addEventListener("install", () => {
  self.skipWaiting();
});
self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});
self.addEventListener("fetch", () => {
  // 不攔截：交給網路。
});
`
