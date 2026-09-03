// Service Worker: hält nur die Programmhuelle vor.
//
// Dateiinhalte werden bewusst NICHT zwischengespeichert - der Cache waere
// sofort veraltet, und niemand will 4-GB-Videos im Browserspeicher.

const CACHE = 'speednas-shell-v1';
const SHELL = [
  '/',
  '/static/css/app.css',
  '/static/js/app.js',
  '/static/js/api.js',
  '/static/js/ui.js',
  '/static/js/util.js',
  '/static/js/upload.js',
  '/static/js/viewer.js',
  '/static/js/settings.js',
  '/static/icons/icon-192.png',
  '/manifest.webmanifest',
];

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET') return;
  const u = new URL(req.url);
  if (u.origin !== location.origin) return;
  // Alles Dynamische geht immer ans Netz.
  if (u.pathname.startsWith('/api/') || u.pathname.startsWith('/s/')) return;

  // Statische Bausteine: erst aus dem Cache liefern, im Hintergrund erneuern.
  if (u.pathname.startsWith('/static/') || u.pathname === '/manifest.webmanifest') {
    e.respondWith(
      caches.match(req).then((hit) => {
        const net = fetch(req).then((res) => {
          if (res.ok) caches.open(CACHE).then((c) => c.put(req, res.clone()));
          return res;
        }).catch(() => hit);
        return hit || net;
      }),
    );
    return;
  }

  // Seiten: Netz zuerst, Cache nur als Notnagel ohne Verbindung.
  if (req.mode === 'navigate') {
    e.respondWith(fetch(req).catch(() => caches.match('/')));
  }
});
