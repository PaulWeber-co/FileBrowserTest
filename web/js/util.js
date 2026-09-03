// Kleine Helfer: DOM, Formatierung, Symbole.
// Bewusst ohne Framework - die Oberfläche soll auch auf einem alten iPhone
// sofort da sein, und jedes Kilobyte lädt hier über WLAN vom eigenen PC.

/** Erzeugt ein Element mit Attributen und Kindern. */
export function el(tag, attrs = {}, ...children) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === null || v === undefined || v === false) continue;
    if (k === 'class') n.className = v;
    else if (k === 'html') n.innerHTML = v;
    else if (k === 'text') n.textContent = v;
    else if (k === 'dataset') Object.assign(n.dataset, v);
    else if (k.startsWith('on') && typeof v === 'function') n.addEventListener(k.slice(2), v);
    else if (v === true) n.setAttribute(k, '');
    else n.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    n.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return n;
}

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

/** Entfernt alle Kinder eines Knotens. */
export function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
  return node;
}

// ------------------------------------------------------------ Symbole ----

const P = {
  folder:   ['M3 7.5A2 2 0 0 1 5 5.5h3.6a2 2 0 0 1 1.6.8l1 1.4H19a2 2 0 0 1 2 2v8.3a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z'],
  file:     ['M14 3.5v4.6a1 1 0 0 0 1 1h4.4', 'M19.5 9.6V19a2 2 0 0 1-2 2h-11a2 2 0 0 1-2-2V5.5a2 2 0 0 1 2-2H14Z'],
  image:    ['M4 6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2Z', 'm5.5 17 4-4.2 3 2.8 3.5-4.6 3 6', 'M9.2 9.3a1.1 1.1 0 1 1-2.2 0 1.1 1.1 0 0 1 2.2 0Z'],
  video:    ['M3.5 7a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2h-9a2 2 0 0 1-2-2Z', 'm16.5 10.5 4-2.6v8.2l-4-2.6Z'],
  audio:    ['M9 17.5V5.8l11-2v11', 'M9 17.5a2.4 2.4 0 1 1-4.8 0 2.4 2.4 0 0 1 4.8 0Z', 'M20 14.8a2.4 2.4 0 1 1-4.8 0 2.4 2.4 0 0 1 4.8 0Z'],
  pdf:      ['M14 3.5v4.6a1 1 0 0 0 1 1h4.4', 'M19.5 9.6V19a2 2 0 0 1-2 2h-11a2 2 0 0 1-2-2V5.5a2 2 0 0 1 2-2H14Z', 'M8.6 17.2c2.6-1.4 4-4 4.6-6.3.3-1.2-1.2-1.6-1.5-.4-.6 2.6 1 6.7 3.9 6.7'],
  text:     ['M14 3.5v4.6a1 1 0 0 0 1 1h4.4', 'M19.5 9.6V19a2 2 0 0 1-2 2h-11a2 2 0 0 1-2-2V5.5a2 2 0 0 1 2-2H14Z', 'M8.5 13h7', 'M8.5 16.4h4.5'],
  doc:      ['M14 3.5v4.6a1 1 0 0 0 1 1h4.4', 'M19.5 9.6V19a2 2 0 0 1-2 2h-11a2 2 0 0 1-2-2V5.5a2 2 0 0 1 2-2H14Z', 'M8.5 13h7', 'M8.5 16.4h7'],
  code:     ['M9.5 9 6 12.5 9.5 16', 'M14.5 9l3.5 3.5-3.5 3.5', 'M4 6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2Z'],
  archive:  ['M3.5 8.5h17', 'M5.5 8.5V6.4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v2.1', 'M5.5 8.5V18a2 2 0 0 0 2 2h9a2 2 0 0 0 2-2V8.5', 'M10.5 12.5h3'],
  other:    ['M14 3.5v4.6a1 1 0 0 0 1 1h4.4', 'M19.5 9.6V19a2 2 0 0 1-2 2h-11a2 2 0 0 1-2-2V5.5a2 2 0 0 1 2-2H14Z'],
  heic:     ['M4 6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2Z', 'm5.5 17 4-4.2 3 2.8 3.5-4.6 3 6'],

  chevronRight: ['m9.5 6 6 6-6 6'],
  chevronLeft:  ['m14.5 6-6 6 6 6'],
  chevronDown:  ['m6 9.5 6 6 6-6'],
  chevronUp:    ['m6 14.5 6-6 6 6'],
  arrowUp:      ['M12 19.5V5', 'm6 10.5 6-6 6 6'],
  arrowLeft:    ['M19.5 12H5', 'm10.5 6-6 6 6 6'],
  search:       ['M11 18.5a7.5 7.5 0 1 0 0-15 7.5 7.5 0 0 0 0 15Z', 'm20.5 20.5-4.2-4.2'],
  close:        ['m6 6 12 12', 'm18 6-12 12'],
  check:        ['m5 12.5 4.5 4.5L19 7.5'],
  plus:         ['M12 5v14', 'M5 12h14'],
  minus:        ['M5 12h14'],
  upload:       ['M12 16V4', 'm7 9 5-5 5 5', 'M4 15v3.5a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V15'],
  download:     ['M12 4v12', 'm7 11 5 5 5-5', 'M4 15v3.5a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V15'],
  trash:        ['M4.5 6.5h15', 'M9.5 6.5V5a1.5 1.5 0 0 1 1.5-1.5h2A1.5 1.5 0 0 1 14.5 5v1.5', 'M6.5 6.5 7.4 19a2 2 0 0 0 2 1.9h5.2a2 2 0 0 0 2-1.9l.9-12.5', 'M10.5 10.5v6', 'M13.5 10.5v6'],
  rename:       ['M4 20h16', 'M14.5 4.5 19 9l-8.5 8.5H6V13Z'],
  copy:         ['M9 9.5a2 2 0 0 1 2-2h7a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2h-7a2 2 0 0 1-2-2Z', 'M15 7.5v-1a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h1'],
  cut:          ['M6.5 8a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5Z', 'M6.5 21a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5Z', 'm8.6 6.8 10.9 10.4', 'M19.5 6.8 8.6 17.2'],
  paste:        ['M9 4.5h6M8 6.5H6.5a2 2 0 0 0-2 2V19a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V8.5a2 2 0 0 0-2-2H16', 'M9 3h6v3.5H9Z'],
  share:        ['M17 8.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5Z', 'M7 15a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5Z', 'M17 21a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5Z', 'm9.2 11.3 5.6-2.8', 'm9.2 13.7 5.6 2.8'],
  star:         ['m12 4 2.5 5.2 5.5.8-4 3.9 1 5.6L12 16.8 7 19.5l1-5.6-4-3.9 5.5-.8Z'],
  grid:         ['M4 5.5h6v6H4Z', 'M14 5.5h6v6h-6Z', 'M4 15.5h6v6H4Z', 'M14 15.5h6v6h-6Z'],
  list:         ['M4 7h16', 'M4 12h16', 'M4 17h16'],
  refresh:      ['M20 11a8 8 0 1 0-1.8 6.1', 'M20 5v6h-6'],
  settings:     ['M12 15.2a3.2 3.2 0 1 0 0-6.4 3.2 3.2 0 0 0 0 6.4Z', 'M19.2 14.4a1.5 1.5 0 0 0 .3 1.7l.1.1a1.8 1.8 0 1 1-2.6 2.6l-.1-.1a1.5 1.5 0 0 0-2.6 1.1v.2a1.8 1.8 0 1 1-3.6 0v-.1a1.5 1.5 0 0 0-2.6-1.1l-.1.1a1.8 1.8 0 1 1-2.6-2.6l.1-.1a1.5 1.5 0 0 0-1.1-2.6H4a1.8 1.8 0 0 1 0-3.6h.1a1.5 1.5 0 0 0 1.1-2.6l-.1-.1a1.8 1.8 0 1 1 2.6-2.6l.1.1a1.5 1.5 0 0 0 2.6-1.1V4a1.8 1.8 0 1 1 3.6 0v.1a1.5 1.5 0 0 0 2.6 1.1l.1-.1a1.8 1.8 0 1 1 2.6 2.6l-.1.1a1.5 1.5 0 0 0 1.1 2.6h.2a1.8 1.8 0 1 1 0 3.6h-.1a1.5 1.5 0 0 0-1.4.9Z'],
  menu:         ['M4 7h16', 'M4 12h16', 'M4 17h16'],
  more:         ['M12 6.5v.01', 'M12 12v.01', 'M12 17.5v.01'],
  info:         ['M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Z', 'M12 11v5', 'M12 8v.01'],
  sun:          ['M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10Z', 'M12 2v2', 'M12 20v2', 'm4.9 4.9 1.4 1.4', 'm17.7 17.7 1.4 1.4', 'M2 12h2', 'M20 12h2', 'm6.3 17.7-1.4 1.4', 'm19.1 4.9-1.4 1.4'],
  moon:         ['M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5Z'],
  home:         ['m4 10.5 8-6.5 8 6.5V19a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2Z', 'M9.5 21v-6.5h5V21'],
  link:         ['M10 13.5a3.5 3.5 0 0 0 5 0l3-3a3.5 3.5 0 0 0-5-5l-1 1', 'M14 10.5a3.5 3.5 0 0 0-5 0l-3 3a3.5 3.5 0 0 0 5 5l1-1'],
  zip:          ['M3.5 8.5h17', 'M5.5 8.5V6.4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v2.1', 'M5.5 8.5V18a2 2 0 0 0 2 2h9a2 2 0 0 0 2-2V8.5', 'M10.5 12.5h3'],
  edit:         ['M4 20h16', 'M14.5 4.5 19 9l-8.5 8.5H6V13Z'],
  eye:          ['M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12Z', 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z'],
  logout:       ['M15 5.5V4a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h7a2 2 0 0 0 2-2v-1.5', 'M19.5 12H9.5', 'm16 8.5 3.5 3.5-3.5 3.5'],
  users:        ['M9 11.5a3.75 3.75 0 1 0 0-7.5 3.75 3.75 0 0 0 0 7.5Z', 'M2.5 20.5a6.5 6.5 0 0 1 13 0', 'M16.5 4.4a3.75 3.75 0 0 1 0 7.2', 'M17.5 14.4a6.5 6.5 0 0 1 4 6.1'],
  gauge:        ['M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Z', 'm12 12 4-4', 'M12 12v.01'],
  server:       ['M3.5 6.5a2 2 0 0 1 2-2h13a2 2 0 0 1 2 2v3a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2Z', 'M3.5 15.5a2 2 0 0 1 2-2h13a2 2 0 0 1 2 2v2a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2Z', 'M7 8h.01', 'M7 16.5h.01'],
  cloud:        ['M17.5 19a4.5 4.5 0 0 0 .5-8.97A6 6 0 0 0 6.1 10.4 4 4 0 0 0 6.5 19Z'],
  hdd:          ['M3.5 12.5 6 5.8a2 2 0 0 1 1.9-1.3h8.2A2 2 0 0 1 18 5.8l2.5 6.7', 'M3.5 12.5v4a2 2 0 0 0 2 2h13a2 2 0 0 0 2-2v-4Z', 'M7 15.5h.01', 'M10.5 15.5h.01'],
  folderPlus:   ['M3 7.5A2 2 0 0 1 5 5.5h3.6a2 2 0 0 1 1.6.8l1 1.4H19a2 2 0 0 1 2 2v8.3a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z', 'M12 11.5v5', 'M9.5 14h5'],
  select:       ['M4.5 7.5V6a1.5 1.5 0 0 1 1.5-1.5h1.5', 'M16.5 4.5H18A1.5 1.5 0 0 1 19.5 6v1.5', 'M19.5 16.5V18a1.5 1.5 0 0 1-1.5 1.5h-1.5', 'M7.5 19.5H6A1.5 1.5 0 0 1 4.5 18v-1.5', 'M10.5 4.5h3', 'M10.5 19.5h3', 'M4.5 10.5v3', 'M19.5 10.5v3'],
  play:         ['m7 4.5 12 7.5-12 7.5Z'],
  clock:        ['M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Z', 'M12 7v5l3 2'],
  wifi:         ['M2.5 9.5a15 15 0 0 1 19 0', 'M5.8 13a10 10 0 0 1 12.4 0', 'M9 16.4a5 5 0 0 1 6 0', 'M12 19.8h.01'],
  shield:       ['M12 3 5 6v5.5c0 4.3 2.9 8.2 7 9.5 4.1-1.3 7-5.2 7-9.5V6Z', 'm9.5 12 1.8 1.8 3.4-3.6'],
};

/** Liefert ein SVG-Symbol als DOM-Knoten. */
export function icon(name, size) {
  const paths = P[name] || P.other;
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '1.7');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  svg.setAttribute('aria-hidden', 'true');
  if (size) { svg.setAttribute('width', size); svg.setAttribute('height', size); }
  for (const d of paths) {
    const p = document.createElementNS(ns, 'path');
    p.setAttribute('d', d);
    svg.appendChild(p);
  }
  return svg;
}

/** Ordnet einer Dateiklasse das passende Symbol zu. */
export function kindIcon(kind) {
  const map = {
    folder: 'folder', image: 'image', heic: 'heic', video: 'video', audio: 'audio',
    pdf: 'pdf', text: 'text', code: 'code', archive: 'archive', doc: 'doc',
  };
  return map[kind] || 'other';
}

// -------------------------------------------------------- Formatierung ---

/** Bytes menschenlesbar - mit den Einheiten, die auch Windows anzeigt. */
export function bytes(n) {
  if (n === null || n === undefined) return '';
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB', 'PB'];
  let v = n / 1024, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

const dtToday = new Intl.DateTimeFormat('de-DE', { hour: '2-digit', minute: '2-digit' });
const dtYear = new Intl.DateTimeFormat('de-DE', { day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit' });
const dtFull = new Intl.DateTimeFormat('de-DE', { day: '2-digit', month: 'short', year: 'numeric' });
const dtExact = new Intl.DateTimeFormat('de-DE', { dateStyle: 'full', timeStyle: 'medium' });

/** Zeitstempel kompakt: heute nur die Uhrzeit, sonst Datum. */
export function when(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d) || d.getFullYear() < 1972) return '';
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return `Heute, ${dtToday.format(d)}`;
  const yest = new Date(now); yest.setDate(now.getDate() - 1);
  if (d.toDateString() === yest.toDateString()) return `Gestern, ${dtToday.format(d)}`;
  if (d.getFullYear() === now.getFullYear()) return dtYear.format(d);
  return dtFull.format(d);
}

export function whenExact(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  return isNaN(d) ? '' : dtExact.format(d);
}

/** Dauer in Sekunden als "2 Min 5 Sek". */
export function duration(sec) {
  if (!isFinite(sec) || sec < 0) return '';
  if (sec < 60) return `${Math.round(sec)} Sek`;
  if (sec < 3600) return `${Math.floor(sec / 60)} Min ${Math.round(sec % 60)} Sek`;
  return `${Math.floor(sec / 3600)} Std ${Math.floor((sec % 3600) / 60)} Min`;
}

export function rate(bps) {
  if (!bps || !isFinite(bps)) return '';
  return `${bytes(bps)}/s`;
}

/** Kürzt lange Namen in der Mitte, damit die Endung sichtbar bleibt. */
export function ellipsisMiddle(s, max = 42) {
  if (s.length <= max) return s;
  const keep = Math.floor((max - 1) / 2);
  return `${s.slice(0, keep)}…${s.slice(-keep)}`;
}

/** Verzögert schnelle Aufrufe (Sucheingabe, Größenänderung). */
export function debounce(fn, ms = 220) {
  let t;
  return (...args) => { clearTimeout(t); t = setTimeout(() => fn(...args), ms); };
}

/** Begrenzt die Aufrufhäufigkeit (Scroll-Ereignisse). */
export function throttle(fn, ms = 100) {
  let last = 0, timer = null;
  return (...args) => {
    const now = Date.now();
    if (now - last >= ms) { last = now; fn(...args); }
    else if (!timer) {
      timer = setTimeout(() => { timer = null; last = Date.now(); fn(...args); }, ms - (now - last));
    }
  };
}

/** Text in die Zwischenablage - mit Rückfall für unsichere Kontexte. */
export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // Ohne HTTPS gibt es die Clipboard-API nicht; dann der alte Weg.
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.cssText = 'position:fixed;opacity:0';
    document.body.appendChild(ta);
    ta.select();
    let ok = false;
    try { ok = document.execCommand('copy'); } catch { ok = false; }
    ta.remove();
    return ok;
  }
}

/** Baut eine URL mit Suchparametern. */
export function url(path, params = {}) {
  const u = new URL(path, location.origin);
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') continue;
    u.searchParams.set(k, v);
  }
  return u.pathname + u.search;
}

/** Letzter Pfadbestandteil. */
export function baseName(p) {
  if (!p) return '';
  const i = p.lastIndexOf('/');
  return i < 0 ? p : p.slice(i + 1);
}

/** Elternpfad. */
export function dirName(p) {
  if (!p) return '';
  const i = p.lastIndexOf('/');
  return i < 0 ? '' : p.slice(0, i);
}

/** Pfadteile zusammensetzen. */
export function joinPath(...parts) {
  return parts.filter(Boolean).join('/').replace(/\/+/g, '/').replace(/^\/|\/$/g, '');
}

export const isTouch = matchMedia('(hover: none)').matches;
export const isMobile = () => matchMedia('(max-width: 900px)').matches;
