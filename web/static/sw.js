/* Service worker (spec 70 section 8). The Go server replaces __ASSET_VERSION__ and __PRECACHE__ when serving /sw.js. */
const VERSION = '__ASSET_VERSION__';
const PRECACHE = typeof __PRECACHE__ === 'undefined' ? [] : __PRECACHE__;
const STATIC_CACHE = `nwr-static-${VERSION}`;
const PAGES_CACHE = 'nwr-pages';
const MEDIA_CACHE = 'nwr-media';
const MAX_PAGES = 20;
const MAX_MEDIA = 150;
const RECIPE_RE = /^\/recipes\/(?!new(?:\/|$))[^/]+(?:\/cook)?\/?$/;
const NEVER_RE = /^\/(auth|api|inbox|healthz|readyz|settings\/tokens)(\/|$)|^\/recipes\/(new|[^/]+\/edit)(\/|$)/;

self.addEventListener('install', (event) => {
  event.waitUntil((async () => {
    const cache = await caches.open(STATIC_CACHE);
    await Promise.allSettled(PRECACHE.map((u) => cache.add(u)));
    await self.skipWaiting();
  })());
});

self.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    const keep = new Set([STATIC_CACHE, PAGES_CACHE, MEDIA_CACHE]);
    for (const k of await caches.keys()) if (!keep.has(k)) await caches.delete(k);
    await self.clients.claim();
  })());
});

self.addEventListener('message', (event) => {
  if (event.data && event.data.type === 'clear-cache') {
    event.waitUntil(Promise.all([caches.delete(PAGES_CACHE), caches.delete(MEDIA_CACHE)]));
  }
});

async function trim(cacheName, max) {
  const cache = await caches.open(cacheName);
  const keys = await cache.keys();
  for (let i = 0; i < keys.length - max; i++) await cache.delete(keys[i]);
}

async function offlinePage() {
  const cache = await caches.open(STATIC_CACHE);
  const hit = (await cache.keys()).find((r) => r.url.endsWith('/offline.html'));
  return hit ? cache.match(hit) : new Response('Offline', { status: 503, headers: { 'Content-Type': 'text/plain' } });
}

async function networkFirstPage(request, url) {
  try {
    const res = await fetch(request);
    if (res.ok && !res.redirected && RECIPE_RE.test(url.pathname)) {
      const cache = await caches.open(PAGES_CACHE);
      await cache.delete(request); // re-insert so insertion order = recency
      await cache.put(request, res.clone());
      await trim(PAGES_CACHE, MAX_PAGES);
    }
    return res;
  } catch {
    const cached = await (await caches.open(PAGES_CACHE)).match(request);
    return cached || offlinePage();
  }
}

async function cacheFirst(cacheName, request, max) {
  const cache = await caches.open(cacheName);
  const hit = await cache.match(request);
  if (hit) return hit;
  const res = await fetch(request);
  if (res.ok && !res.redirected) { await cache.put(request, res.clone()); if (max) await trim(cacheName, max); }
  return res;
}

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  if (NEVER_RE.test(url.pathname)) return;
  if (url.pathname.startsWith('/static/') || /^\/icon-\d+\.png$/.test(url.pathname) || url.pathname === '/manifest.webmanifest') {
    event.respondWith(cacheFirst(STATIC_CACHE, req).catch(() => fetch(req)));
    return;
  }
  if (url.pathname.startsWith('/media/')) {
    event.respondWith(cacheFirst(MEDIA_CACHE, req, MAX_MEDIA).catch(() => Response.error()));
    return;
  }
  if (req.mode === 'navigate' || (req.headers.get('accept') || '').includes('text/html')) {
    if (req.headers.get('HX-Request')) return;
    event.respondWith(networkFirstPage(req, url));
  }
});
