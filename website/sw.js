// Service worker tối giản — KHÔNG cache gì cả (network-only).
// Mục đích: giúp app cài được lên màn hình chính điện thoại (installable),
// nhưng luôn lấy dữ liệu/giao diện mới nhất từ server → không bao giờ bị kẹt bản cũ.
self.addEventListener('install', e => self.skipWaiting());
self.addEventListener('activate', e => e.waitUntil(self.clients.claim()));
self.addEventListener('fetch', e => {
  // Bỏ qua WebSocket và mọi request khác method GET
  e.respondWith(fetch(e.request).catch(() =>
    new Response('Mất kết nối mạng', { status: 503, headers: { 'Content-Type': 'text/plain; charset=utf-8' } })
  ));
});
