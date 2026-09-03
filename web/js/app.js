// SpeedNAS - Hauptanwendung.

import {
  el, $, $$, clear, icon, kindIcon, bytes, when, whenExact, url,
  baseName, dirName, joinPath, debounce, copyText, isMobile, isTouch, rate,
} from './util.js';
import * as api from './api.js';
import {
  toast, toastOk, showError, dialog, confirm, prompt, menu, closeMenu,
  field, input, select, checkbox, kv, busy, emptyState,
} from './ui.js';
import { UploadQueue, readDataTransfer, pickFiles } from './upload.js';
import { openViewer, canView } from './viewer.js';
import { openSettings } from './settings.js';

const app = {
  state: {
    me: null,
    locations: [],
    rawLocations: [],
    favorites: [],
    recent: [],
    loc: null,
    path: '',
    entries: [],
    space: null,
    selection: new Set(),
    anchor: -1,
    view: 'list',
    sort: 'name',
    desc: false,
    showHidden: false,
    thumbs: true,
    clipboard: null,
    search: null,
    loading: false,
    readOnly: false,
  },
  reloadLocations,
};

const dom = {};
let uploads;
let jobsStop = null;

// ============================================================== Start ====

init().catch((err) => showError(err, 'Start fehlgeschlagen'));

async function init() {
  cacheDom();
  applyTheme(localStorage.getItem('theme') || 'auto');

  const me = await api.get('/api/me');
  app.state.me = me;
  if (!me.authenticated) { location.href = '/login'; return; }

  const p = me.prefs || {};
  app.state.view = p.view || 'list';
  app.state.sort = p.sort || 'name';
  app.state.desc = !!p.desc;
  app.state.showHidden = !!p.showHidden;
  app.state.thumbs = p.thumbs !== false;
  if (p.theme) applyTheme(p.theme);

  uploads = new UploadQueue({
    onUpdate: renderTransfers,
    onFileDone: debounce(() => { if (!app.state.search) refresh(true); }, 700),
  });

  wireChrome();
  wireKeyboard();
  wireDragDrop();

  await Promise.all([reloadLocations(), reloadFavorites()]);

  window.addEventListener('hashchange', routeFromHash);
  routeFromHash();

  subscribeJobs();
  registerServiceWorker();
}

/** Füllt alle Knöpfe mit data-icon="name" mit dem passenden Symbol. */
function hydrateIcons(root = document) {
  for (const node of root.querySelectorAll('[data-icon]')) {
    const size = Number(node.dataset.iconSize || 18);
    node.prepend(icon(node.dataset.icon, size));
    delete node.dataset.icon;
  }
}

function cacheDom() {
  hydrateIcons();
  dom.sidebar = $('#sidebar');
  dom.navLocations = $('#nav-locations');
  dom.navFavorites = $('#nav-favorites');
  dom.navRecent = $('#nav-recent');
  dom.storage = $('#storage');
  dom.crumbs = $('#crumbs');
  dom.content = $('#content');
  dom.toolbar = $('#toolbar');
  dom.selInfo = $('#sel-info');
  dom.searchInput = $('#search-input');
  dom.searchBox = $('#search-box');
  dom.transfers = $('#transfers');
  dom.loading = $('#loading');
  dom.userBtn = $('#user-btn');
}

// ============================================================ Routing ====

function routeFromHash() {
  const raw = decodeURIComponent(location.hash.replace(/^#/, ''));
  const [locId, ...rest] = raw.split('/').filter((s, i) => i > 0 || s !== '');
  const path = rest.join('/');
  const loc = app.state.locations.find((l) => l.id === locId) || app.state.locations[0];
  if (!loc) { renderNoLocations(); return; }
  app.state.loc = loc;
  app.state.path = path || '';
  app.state.search = null;
  dom.searchInput.value = '';
  dom.searchBox.classList.remove('filled');
  renderSidebar();
  load();
}

function navigate(locId, path, { replace = false } = {}) {
  const hash = `#/${locId}${path ? '/' + path : ''}`;
  if (location.hash === hash) { load(); return; }
  if (replace) history.replaceState(null, '', hash);
  else location.hash = hash;
  if (replace) routeFromHash();
}

// ========================================================= Datenladen ====

async function reloadLocations() {
  const data = await api.get('/api/locations');
  app.state.locations = data.locations;
  if (app.state.me?.user?.admin) {
    try {
      const adm = await api.get('/api/admin/locations');
      app.state.rawLocations = adm.locations;
    } catch { /* nicht kritisch */ }
  }
  renderSidebar();
  if (!app.state.locations.length) renderNoLocations();
  else if (!app.state.loc) routeFromHash();
}

async function reloadFavorites() {
  try {
    const data = await api.get('/api/favorites');
    app.state.favorites = data.favorites || [];
    app.state.recent = data.recent || [];
    renderSidebar();
  } catch { /* Lesezeichen sind Beiwerk */ }
}

let loadToken = 0;
async function load(silent = false) {
  const loc = app.state.loc;
  if (!loc) return;
  const token = ++loadToken;
  app.state.selection.clear();
  if (!silent) setLoading(true);

  try {
    const data = await api.get(url('/api/list', {
      loc: loc.id, path: app.state.path,
      sort: app.state.sort, desc: app.state.desc ? 1 : '',
      hidden: app.state.showHidden ? 1 : '',
      space: 1,
    }));
    if (token !== loadToken) return;
    app.state.entries = data.entries || [];
    app.state.space = data.space || null;
    app.state.readOnly = data.readOnly;
    renderCrumbs(data.crumbs || []);
    renderContent();
    renderStorage();
    renderToolbar();
  } catch (err) {
    if (token !== loadToken) return;
    renderLoadError(err);
  } finally {
    if (token === loadToken) setLoading(false);
  }
}

function refresh(silent = false) {
  if (app.state.search) return;
  const loc = app.state.loc;
  if (!loc) return;
  api.get(url('/api/list', { loc: loc.id, path: app.state.path, refresh: 1, sort: app.state.sort, desc: app.state.desc ? 1 : '', hidden: app.state.showHidden ? 1 : '', space: 1 }))
    .then((data) => {
      app.state.entries = data.entries || [];
      app.state.space = data.space || null;
      renderContent();
      renderStorage();
    })
    .catch((err) => { if (!silent) showError(err, 'Aktualisieren fehlgeschlagen'); });
}

function setLoading(on) {
  app.state.loading = on;
  dom.loading.style.display = on ? 'block' : 'none';
  if (on) {
    clear(dom.content);
    dom.content.append(skeletonList());
  }
}

function skeletonList() {
  const wrap = el('div', { class: 'rows' });
  for (let i = 0; i < 10; i++) {
    wrap.append(el('div', { class: 'row-item' },
      el('div', { class: 'skeleton', style: 'width:18px;height:18px;border-radius:5px' }),
      el('div', { class: 'cell-name' },
        el('div', { class: 'skeleton', style: 'width:26px;height:26px' }),
        el('div', { class: 'skeleton', style: `width:${120 + (i * 37) % 200}px;height:12px` })),
      el('div', { class: 'skeleton cell-size', style: 'width:52px;height:11px;justify-self:end' }),
      el('div', { class: 'skeleton cell-date', style: 'width:110px;height:11px' }),
      el('div'),
    ));
  }
  return wrap;
}

// =========================================================== Chrome ======

function wireChrome() {
  $('#btn-menu')?.addEventListener('click', () => {
    dom.sidebar.classList.add('open');
    const scrim = el('div', { class: 'scrim', onclick: () => { dom.sidebar.classList.remove('open'); scrim.remove(); } });
    document.body.append(scrim);
  });

  $('#btn-up')?.addEventListener('click', goUp);
  $('#btn-refresh')?.addEventListener('click', () => refresh());
  $('#btn-newfolder')?.addEventListener('click', actionNewFolder);
  $('#btn-upload')?.addEventListener('click', () => doUpload(false));
  $('#btn-upload-dir')?.addEventListener('click', () => doUpload(true));
  $('#btn-settings')?.addEventListener('click', () => openSettings(app));

  // Untere Leiste auf dem Handy - dieselben Aktionen, nur daumenfreundlich.
  $('#mb-places')?.addEventListener('click', () => $('#btn-menu').click());
  $('#mb-up')?.addEventListener('click', goUp);
  $('#mb-upload')?.addEventListener('click', () => doUpload(false));
  $('#mb-search')?.addEventListener('click', () => { dom.searchInput.focus(); dom.searchInput.select(); });
  $('#mb-more')?.addEventListener('click', (e) => {
    const r = e.currentTarget.getBoundingClientRect();
    menu(r.left - 120, r.top - 260, moreMenuItems());
  });
  $('#btn-theme')?.addEventListener('click', cycleTheme);
  $('#btn-view')?.addEventListener('click', toggleView);
  $('#btn-download')?.addEventListener('click', actionDownload);
  $('#btn-delete')?.addEventListener('click', actionDelete);
  $('#btn-more')?.addEventListener('click', (e) => {
    const r = e.currentTarget.getBoundingClientRect();
    menu(r.left, r.bottom + 4, moreMenuItems());
  });
  $('#btn-clear-sel')?.addEventListener('click', () => { app.state.selection.clear(); renderContent(); });

  dom.userBtn?.addEventListener('click', (e) => {
    const r = e.currentTarget.getBoundingClientRect();
    menu(r.right - 200, r.bottom + 6, [
      { label: `Angemeldet als ${app.state.me.user.name}`, icon: 'users', disabled: true },
      'separator',
      { label: 'Einstellungen', icon: 'settings', onClick: () => openSettings(app) },
      { label: 'Tastenkürzel', icon: 'info', onClick: () => openSettings(app, 'about') },
      'separator',
      { label: 'Abmelden', icon: 'logout', danger: true, onClick: doLogout },
    ]);
  });

  dom.searchInput.addEventListener('input', debounce(() => {
    const q = dom.searchInput.value.trim();
    dom.searchBox.classList.toggle('filled', q.length > 0);
    if (q.length >= 2) startSearch(q);
    else if (!q) { app.state.search = null; load(); }
  }, 350));
  dom.searchInput.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') { dom.searchInput.value = ''; dom.searchBox.classList.remove('filled'); app.state.search = null; load(); dom.searchInput.blur(); }
  });
  $('#search-clear')?.addEventListener('click', () => {
    dom.searchInput.value = '';
    dom.searchBox.classList.remove('filled');
    app.state.search = null;
    load();
  });

  // Klick ins Leere hebt die Auswahl auf.
  dom.content.addEventListener('mousedown', (e) => {
    if (e.target === dom.content || e.target.classList.contains('rows') || e.target.classList.contains('grid')) {
      app.state.selection.clear();
      renderContent();
    }
  });
  dom.content.addEventListener('contextmenu', (e) => {
    if (e.target.closest('.row-item, .tile')) return;
    e.preventDefault();
    menu(e.clientX, e.clientY, backgroundMenuItems());
  });
}

function cycleTheme() {
  const order = ['auto', 'light', 'dark'];
  const cur = localStorage.getItem('theme') || 'auto';
  const next = order[(order.indexOf(cur) + 1) % order.length];
  applyTheme(next);
  savePrefs();
  toast(`Design: ${{ auto: 'automatisch', light: 'hell', dark: 'dunkel' }[next]}`, { timeout: 1400 });
}

function applyTheme(theme) {
  localStorage.setItem('theme', theme);
  if (theme === 'auto') document.documentElement.removeAttribute('data-theme');
  else document.documentElement.setAttribute('data-theme', theme);
  const dark = theme === 'dark' || (theme === 'auto' && matchMedia('(prefers-color-scheme: dark)').matches);
  $('#btn-theme') && clear($('#btn-theme')).append(icon(dark ? 'moon' : 'sun', 18));
  document.querySelector('meta[name=theme-color]')?.setAttribute('content', dark ? '#0f1218' : '#ffffff');
}

function toggleView() {
  app.state.view = app.state.view === 'list' ? 'grid' : 'list';
  renderContent();
  renderToolbar();
  savePrefs();
}

const savePrefs = debounce(() => {
  api.post('/api/prefs', {
    view: app.state.view, sort: app.state.sort, desc: app.state.desc,
    showHidden: app.state.showHidden, thumbs: app.state.thumbs,
    theme: localStorage.getItem('theme') || 'auto',
  }).catch(() => {});
}, 500);

async function doLogout() {
  try { await api.post('/api/logout'); } catch { /* egal */ }
  location.href = '/login';
}

// =========================================================== Sidebar =====

function renderSidebar() {
  const s = app.state;

  clear(dom.navLocations);
  for (const loc of s.locations) {
    dom.navLocations.append(el('button', {
      class: `nav-item${s.loc?.id === loc.id && !s.search ? ' active' : ''}`,
      onclick: () => { navigate(loc.id, ''); closeSidebar(); },
      oncontextmenu: (e) => {
        e.preventDefault();
        menu(e.clientX, e.clientY, [
          { label: 'Öffnen', icon: 'folder', onClick: () => navigate(loc.id, '') },
          s.me?.user?.admin ? { label: 'Bearbeiten', icon: 'settings', onClick: () => openSettings(app) } : null,
        ].filter(Boolean));
      },
    },
      el('span', { class: 'icon' }, icon(loc.type === 'local' ? 'hdd' : 'server', 18)),
      el('span', { class: 'label' },
        loc.label,
        el('span', { class: 'sub', text: loc.detail || loc.type.toUpperCase() }),
      ),
    ));
  }
  if (s.me?.user?.admin) {
    dom.navLocations.append(el('button', {
      class: 'nav-item', style: 'color:var(--accent)',
      onclick: () => openSettings(app),
    }, el('span', { class: 'icon' }, icon('plus', 18)), el('span', { class: 'label' }, 'Standort hinzufügen')));
  }

  clear(dom.navFavorites);
  const favWrap = dom.navFavorites.closest('.nav-group');
  favWrap.style.display = s.favorites.length ? '' : 'none';
  for (const f of s.favorites) {
    const loc = s.locations.find((l) => l.id === f.locationId);
    dom.navFavorites.append(el('button', {
      class: `nav-item${s.loc?.id === f.locationId && s.path === f.path && !s.search ? ' active' : ''}`,
      onclick: () => { navigate(f.locationId, f.path); closeSidebar(); },
    },
      el('span', { class: 'icon' }, icon('star', 18)),
      el('span', { class: 'label' }, f.name, el('span', { class: 'sub', text: loc?.label || '' })),
      el('span', {
        class: 'remove', title: 'Lesezeichen entfernen',
        onclick: async (e) => {
          e.stopPropagation();
          await api.del('/api/favorites', { locationId: f.locationId, path: f.path });
          reloadFavorites();
        },
      }, icon('close', 14)),
    ));
  }

  clear(dom.navRecent);
  const recWrap = dom.navRecent.closest('.nav-group');
  const recent = s.recent.filter((r) => s.locations.some((l) => l.id === r.locationId)).slice(0, 6);
  recWrap.style.display = recent.length ? '' : 'none';
  for (const f of recent) {
    dom.navRecent.append(el('button', {
      class: 'nav-item',
      onclick: () => { navigate(f.locationId, f.path); closeSidebar(); },
    },
      el('span', { class: 'icon' }, icon('clock', 18)),
      el('span', { class: 'label' }, f.name || 'Wurzel'),
    ));
  }
}

function closeSidebar() {
  dom.sidebar.classList.remove('open');
  $('.scrim')?.remove();
}

function renderStorage() {
  const sp = app.state.space;
  clear(dom.storage);
  if (!sp || !sp.total) { dom.storage.style.display = 'none'; return; }
  dom.storage.style.display = '';
  const used = sp.total - sp.free;
  const pct = Math.min(100, Math.round((used / sp.total) * 100));
  dom.storage.append(
    el('div', { style: 'font-size:11.5px;color:var(--fg-subtle)' },
      `${bytes(sp.free)} frei von ${bytes(sp.total)}`),
    el('div', { class: `storage-bar${pct > 92 ? ' full' : pct > 80 ? ' warn' : ''}` }, el('i', { style: `width:${pct}%` })),
  );
}

// ========================================================= Brotkrumen ====

function renderCrumbs(crumbs) {
  clear(dom.crumbs);
  const loc = app.state.loc;
  dom.crumbs.append(el('button', {
    class: `crumb${crumbs.length ? '' : ' current'}`,
    onclick: () => navigate(loc.id, ''),
  }, loc.label));
  crumbs.forEach((c, i) => {
    dom.crumbs.append(el('span', { class: 'crumb-sep' }, icon('chevronRight', 14)));
    dom.crumbs.append(el('button', {
      class: `crumb${i === crumbs.length - 1 ? ' current' : ''}`,
      onclick: () => navigate(loc.id, c.path),
    }, c.name));
  });
  dom.crumbs.scrollLeft = dom.crumbs.scrollWidth;
}

// ========================================================== Werkzeuge ====

function renderToolbar() {
  const n = app.state.selection.size;
  const ro = app.state.readOnly;
  dom.selInfo.textContent = n ? `${n} ausgewählt` : '';
  $('#sel-actions').style.display = n ? 'flex' : 'none';
  $('#normal-actions').style.display = n ? 'none' : 'flex';
  for (const id of ['btn-newfolder', 'btn-upload', 'btn-upload-dir']) {
    const b = $('#' + id);
    if (b) b.style.display = ro ? 'none' : '';
  }
  const vb = $('#btn-view');
  if (vb) clear(vb).append(icon(app.state.view === 'list' ? 'grid' : 'list', 18));
  $('#btn-up').disabled = !app.state.path;
}

function moreMenuItems() {
  const s = app.state;
  const isFav = s.favorites.some((f) => f.locationId === s.loc?.id && f.path === s.path);
  return [
    { label: 'Alles auswählen', icon: 'select', shortcut: 'Strg+A', onClick: selectAll },
    { label: s.showHidden ? 'Versteckte Dateien ausblenden' : 'Versteckte Dateien anzeigen', icon: 'eye', onClick: toggleHidden },
    { label: s.thumbs ? 'Vorschaubilder aus' : 'Vorschaubilder an', icon: 'image', onClick: toggleThumbs },
    'separator',
    {
      label: isFav ? 'Lesezeichen entfernen' : 'Als Lesezeichen merken',
      icon: 'star',
      onClick: async () => {
        const body = { locationId: s.loc.id, path: s.path, name: baseName(s.path) || s.loc.label };
        if (isFav) await api.del('/api/favorites', body); else await api.post('/api/favorites', body);
        reloadFavorites();
      },
    },
    { label: 'Ordner als ZIP laden', icon: 'zip', onClick: () => downloadZip([]) },
    s.clipboard && !s.readOnly ? { label: `Einfügen (${s.clipboard.items.length})`, icon: 'paste', shortcut: 'Strg+V', onClick: actionPaste } : null,
    'separator',
    { label: 'Einstellungen', icon: 'settings', onClick: () => openSettings(app) },
  ].filter(Boolean);
}

function backgroundMenuItems() {
  const s = app.state;
  return [
    !s.readOnly ? { label: 'Neuer Ordner', icon: 'folderPlus', onClick: actionNewFolder } : null,
    !s.readOnly ? { label: 'Dateien hochladen', icon: 'upload', onClick: () => doUpload(false) } : null,
    s.clipboard && !s.readOnly ? { label: `Einfügen (${s.clipboard.items.length})`, icon: 'paste', onClick: actionPaste } : null,
    'separator',
    { label: 'Aktualisieren', icon: 'refresh', shortcut: 'F5', onClick: () => refresh() },
    { label: 'Alles auswählen', icon: 'select', onClick: selectAll },
  ].filter(Boolean);
}

function toggleHidden() { app.state.showHidden = !app.state.showHidden; savePrefs(); load(); }
function toggleThumbs() { app.state.thumbs = !app.state.thumbs; savePrefs(); renderContent(); }

// =========================================================== Inhalt ======

function renderNoLocations() {
  clear(dom.crumbs);
  clear(dom.content);
  dom.content.append(emptyState('server', 'Noch kein Speicherort eingerichtet',
    'Verbinde SpeedNAS mit dem Netzwerkspeicher an deinem Router. Für den Speedport ist das eine SMB-Freigabe.',
    app.state.me?.user?.admin
      ? el('button', { class: 'btn btn-primary', style: 'margin-top:8px', onclick: () => openSettings(app) },
        icon('plus', 17), 'Speicherort hinzufügen')
      : null));
}

function renderLoadError(err) {
  clear(dom.content);
  const box = emptyState('info', 'Der Ordner ließ sich nicht öffnen', err.message);
  box.append(el('div', { style: 'display:flex;gap:8px;margin-top:6px' },
    el('button', { class: 'btn', onclick: () => load() }, icon('refresh', 16), 'Erneut versuchen'),
    app.state.path ? el('button', { class: 'btn', onclick: goUp }, icon('arrowUp', 16), 'Eine Ebene hoch') : null,
    app.state.me?.user?.admin
      ? el('button', { class: 'btn', onclick: () => openSettings(app, 'diagnose') }, icon('shield', 16), 'Diagnose')
      : null,
  ));
  dom.content.append(box);
}

function renderContent() {
  const s = app.state;
  clear(dom.content);

  if (s.search) { renderSearchResults(); return; }
  if (!s.entries.length) {
    dom.content.append(emptyState('folder', 'Dieser Ordner ist leer',
      s.readOnly ? null : 'Zieh Dateien hierher, um sie hochzuladen.'));
    return;
  }

  if (s.view === 'grid') renderGrid();
  else renderList();
  renderToolbar();
}

function renderList() {
  const s = app.state;
  dom.content.append(listHead());
  const rows = el('div', { class: 'rows' });
  s.entries.forEach((e, i) => rows.append(rowFor(e, i)));
  dom.content.append(rows);
}

function listHead() {
  const s = app.state;
  const sortBtn = (key, label, cls) => el('button', {
    class: `${s.sort === key ? 'sorted' : ''}${s.sort === key && s.desc ? ' desc' : ''}`,
    onclick: () => {
      if (s.sort === key) s.desc = !s.desc; else { s.sort = key; s.desc = false; }
      savePrefs(); load();
    },
  }, label, el('span', { class: 'sort-arrow', style: 'display:flex' }, icon('chevronUp', 13)));

  const allSelected = s.entries.length > 0 && s.selection.size === s.entries.length;
  return el('div', { class: 'list-head' },
    el('div', {},
      el('div', {
        class: 'pick', role: 'checkbox', 'aria-checked': String(allSelected),
        style: allSelected ? 'background:var(--accent);border-color:var(--accent)' : '',
        onclick: () => { allSelected ? s.selection.clear() : selectAll(); renderContent(); },
      }, icon('check', 12)),
    ),
    sortBtn('name', 'Name'),
    el('div', { class: 'col-size', style: 'text-align:right' }, sortBtn('size', 'Größe')),
    el('div', { class: 'col-date' }, sortBtn('mtime', 'Geändert')),
    el('div'),
  );
}

function rowFor(e, index) {
  const s = app.state;
  const selected = s.selection.has(e.name);
  const path = joinPath(s.path, e.name);

  const node = el('div', {
    class: `row-item${selected ? ' selected' : ''}${s.clipboard?.op === 'cut' && s.clipboard.loc === s.loc.id && s.clipboard.items.includes(path) ? ' cut' : ''}`,
    tabindex: '0',
    dataset: { name: e.name, index: String(index) },
  },
    el('div', { onclick: (ev) => { ev.stopPropagation(); togglePick(e, index, ev); } },
      el('div', { class: 'pick' }, icon('check', 12))),
    el('div', { class: 'cell-name' },
      thumbOrIcon(e, path),
      el('div', { style: 'min-width:0' },
        el('div', { class: 'name', text: e.name, title: e.name }),
        // Auf schmalen Bildschirmen gibt es keine eigenen Spalten mehr -
        // Größe und Datum wandern unter den Namen.
        el('div', { class: 'row-sub', text: e.dir ? when(e.mtime) : `${bytes(e.size)} · ${when(e.mtime)}` }),
      ),
    ),
    el('div', { class: 'cell-size', text: e.dir ? '' : bytes(e.size) }),
    el('div', { class: 'cell-date', text: when(e.mtime), title: whenExact(e.mtime) }),
    el('div', { class: 'cell-menu' },
      el('button', {
        class: 'btn btn-icon', title: 'Aktionen',
        onclick: (ev) => {
          ev.stopPropagation();
          if (!s.selection.has(e.name)) { s.selection.clear(); s.selection.add(e.name); renderContent(); }
          const r = ev.currentTarget.getBoundingClientRect();
          menu(r.left - 170, r.bottom + 4, itemMenuItems(e));
        },
      }, icon('more', 18)),
    ),
  );

  attachItemEvents(node, e, index);
  return node;
}

function renderGrid() {
  const s = app.state;
  const grid = el('div', { class: 'grid' });
  s.entries.forEach((e, i) => {
    const path = joinPath(s.path, e.name);
    const selected = s.selection.has(e.name);
    const node = el('div', {
      class: `tile${selected ? ' selected' : ''}`,
      tabindex: '0',
      dataset: { name: e.name, index: String(i) },
    },
      el('div', {
        class: 'pick', onclick: (ev) => { ev.stopPropagation(); togglePick(e, i, ev); },
      }, icon('check', 12)),
      el('button', {
        class: 'btn btn-icon tile-menu', title: 'Aktionen',
        onclick: (ev) => {
          ev.stopPropagation();
          if (!s.selection.has(e.name)) { s.selection.clear(); s.selection.add(e.name); renderContent(); }
          const r = ev.currentTarget.getBoundingClientRect();
          menu(r.left - 170, r.bottom + 4, itemMenuItems(e));
        },
      }, icon('more', 17)),
      el('div', { class: 'preview' }, tilePreview(e, path)),
      el('div', { class: 'meta' },
        el('div', { class: 'name', text: e.name, title: e.name }),
        el('div', { class: 'sub', text: e.dir ? 'Ordner' : bytes(e.size) }),
      ),
    );
    attachItemEvents(node, e, i);
    grid.append(node);
  });
  dom.content.append(grid);
}

function thumbOrIcon(e, path) {
  if (!e.dir && e.thumb && app.state.thumbs) {
    const box = el('div', { class: 'thumb-box' });
    const img = el('img', {
      loading: 'lazy', decoding: 'async', alt: '',
      src: url('/api/thumb', { loc: app.state.loc.id, path, w: 160 }),
    });
    img.addEventListener('error', () => { clear(box).replaceWith(iconFor(e)); });
    box.append(img);
    return box;
  }
  return iconFor(e);
}

function tilePreview(e, path) {
  if (!e.dir && e.thumb && app.state.thumbs) {
    const img = el('img', {
      loading: 'lazy', decoding: 'async', alt: '',
      src: url('/api/thumb', { loc: app.state.loc.id, path, w: 320 }),
    });
    img.addEventListener('error', () => img.replaceWith(iconFor(e)));
    return img;
  }
  return iconFor(e);
}

function iconFor(e) {
  const kind = e.dir ? 'folder' : e.kind;
  return el('div', { class: `fico ${kind}` }, icon(kindIcon(kind)));
}

function attachItemEvents(node, e, index) {
  const s = app.state;
  let pressTimer = null;

  node.addEventListener('click', (ev) => {
    if (ev.target.closest('.pick, .btn')) return;
    if (ev.shiftKey && s.anchor >= 0) { selectRange(s.anchor, index); renderContent(); return; }
    if (ev.ctrlKey || ev.metaKey) { togglePick(e, index, ev); return; }
    // Auf Touch-Geräten öffnet ein einfacher Tipp, am Desktop ein
    // Doppelklick - beides ist dort jeweils das Erwartete.
    if (isTouch) { openEntry(e, index); return; }
    s.selection.clear();
    s.selection.add(e.name);
    s.anchor = index;
    renderContent();
  });

  node.addEventListener('dblclick', (ev) => {
    if (ev.target.closest('.pick, .btn')) return;
    openEntry(e, index);
  });

  node.addEventListener('contextmenu', (ev) => {
    ev.preventDefault();
    if (!s.selection.has(e.name)) { s.selection.clear(); s.selection.add(e.name); s.anchor = index; renderContent(); }
    menu(ev.clientX, ev.clientY, itemMenuItems(e));
  });

  node.addEventListener('keydown', (ev) => {
    if (ev.key === 'Enter') { ev.preventDefault(); openEntry(e, index); }
  });

  // Langes Antippen öffnet auf dem Handy das Kontextmenü.
  if (isTouch) {
    node.addEventListener('touchstart', (ev) => {
      pressTimer = setTimeout(() => {
        pressTimer = null;
        const t = ev.touches[0];
        if (!s.selection.has(e.name)) { s.selection.clear(); s.selection.add(e.name); renderContent(); }
        if (navigator.vibrate) navigator.vibrate(12);
        menu(t.clientX, t.clientY, itemMenuItems(e));
      }, 520);
    }, { passive: true });
    const cancel = () => { if (pressTimer) { clearTimeout(pressTimer); pressTimer = null; } };
    node.addEventListener('touchend', cancel);
    node.addEventListener('touchmove', cancel, { passive: true });
    node.addEventListener('touchcancel', cancel);
  }
}

function togglePick(e, index, ev) {
  const s = app.state;
  if (ev?.shiftKey && s.anchor >= 0) { selectRange(s.anchor, index); }
  else {
    s.selection.has(e.name) ? s.selection.delete(e.name) : s.selection.add(e.name);
    s.anchor = index;
  }
  renderContent();
}

function selectRange(a, b) {
  const [from, to] = a <= b ? [a, b] : [b, a];
  for (let i = from; i <= to; i++) {
    const e = app.state.entries[i];
    if (e) app.state.selection.add(e.name);
  }
}

function selectAll() {
  for (const e of app.state.entries) app.state.selection.add(e.name);
  renderContent();
}

function openEntry(e, index) {
  const s = app.state;
  if (e.dir) { navigate(s.loc.id, joinPath(s.path, e.name)); return; }
  if (canView(e)) {
    openViewer({
      loc: s.loc.id, dir: s.path, entries: s.entries, index,
      readOnly: s.readOnly, onChanged: () => refresh(true),
    });
    return;
  }
  downloadOne(joinPath(s.path, e.name), e.name);
}

// =========================================================== Aktionen ====

function selectedPaths() {
  return Array.from(app.state.selection).map((n) => joinPath(app.state.path, n));
}
function selectedEntries() {
  return app.state.entries.filter((e) => app.state.selection.has(e.name));
}

function itemMenuItems(e) {
  const s = app.state;
  const many = s.selection.size > 1;
  const path = joinPath(s.path, e.name);
  return [
    { label: e.dir ? 'Öffnen' : (canView(e) ? 'Ansehen' : 'Herunterladen'), icon: e.dir ? 'folder' : 'eye', onClick: () => openEntry(e, s.entries.indexOf(e)) },
    { label: many ? `${s.selection.size} Objekte herunterladen` : 'Herunterladen', icon: 'download', onClick: actionDownload },
    'separator',
    { label: 'Kopieren', icon: 'copy', shortcut: 'Strg+C', onClick: () => setClipboard('copy') },
    s.readOnly ? null : { label: 'Ausschneiden', icon: 'cut', shortcut: 'Strg+X', onClick: () => setClipboard('cut') },
    s.clipboard && !s.readOnly ? { label: `Einfügen (${s.clipboard.items.length})`, icon: 'paste', onClick: actionPaste } : null,
    'separator',
    s.readOnly || many ? null : { label: 'Umbenennen', icon: 'rename', shortcut: 'F2', onClick: () => actionRename(e) },
    { label: 'Link freigeben', icon: 'share', onClick: () => actionShare(path, e) },
    { label: 'Pfad kopieren', icon: 'link', onClick: async () => (await copyText(path)) && toastOk('Pfad kopiert.') },
    { label: 'Eigenschaften', icon: 'info', onClick: () => showProperties(e, path) },
    'separator',
    s.readOnly ? null : { label: many ? `${s.selection.size} Objekte löschen` : 'Löschen', icon: 'trash', shortcut: 'Entf', danger: true, onClick: actionDelete },
  ].filter(Boolean);
}

function goUp() {
  const s = app.state;
  if (!s.path) return;
  navigate(s.loc.id, dirName(s.path));
}

async function actionNewFolder() {
  const name = await prompt({
    title: 'Neuer Ordner', label: 'Name', value: 'Neuer Ordner',
    confirmLabel: 'Erstellen',
  });
  if (!name) return;
  try {
    await api.post('/api/mkdir', { loc: app.state.loc.id, path: app.state.path, name });
    toastOk(`Ordner "${name}" erstellt.`);
    refresh();
  } catch (err) { showError(err, 'Ordner anlegen fehlgeschlagen'); }
}

async function actionRename(entry) {
  const e = entry || selectedEntries()[0];
  if (!e) return;
  const name = await prompt({
    title: 'Umbenennen', label: 'Neuer Name', value: e.name,
    confirmLabel: 'Umbenennen', selectStem: !e.dir,
  });
  if (!name || name === e.name) return;
  try {
    await api.post('/api/rename', { loc: app.state.loc.id, path: joinPath(app.state.path, e.name), name });
    toastOk('Umbenannt.');
    refresh();
  } catch (err) { showError(err, 'Umbenennen fehlgeschlagen'); }
}

async function actionDelete() {
  const items = selectedPaths();
  if (!items.length) return;
  const entries = selectedEntries();
  const hasDir = entries.some((e) => e.dir);
  const ok = await confirm({
    title: items.length === 1 ? 'Löschen' : `${items.length} Objekte löschen`,
    message: items.length === 1
      ? `"${baseName(items[0])}" endgültig löschen?`
      : `${items.length} Objekte endgültig löschen?`,
    detail: hasDir ? 'Ordner werden mitsamt Inhalt gelöscht. Es gibt keinen Papierkorb - das lässt sich nicht rückgängig machen.'
      : 'Es gibt keinen Papierkorb - das lässt sich nicht rückgängig machen.',
    confirmLabel: 'Löschen', danger: true,
  });
  if (!ok) return;
  try {
    const res = await api.post('/api/delete', { loc: app.state.loc.id, items });
    if (res.failed?.length) {
      toast(res.failed.join('\n'), { type: 'error', title: 'Teilweise fehlgeschlagen' });
    } else {
      toastOk(items.length === 1 ? 'Gelöscht.' : `${items.length} Objekte gelöscht.`);
    }
    app.state.selection.clear();
    refresh();
  } catch (err) { showError(err, 'Löschen fehlgeschlagen'); }
}

function downloadOne(path, name) {
  const a = el('a', { href: url('/api/download', { loc: app.state.loc.id, path }), download: name || baseName(path) });
  document.body.append(a);
  a.click();
  a.remove();
}

function downloadZip(items) {
  const params = { loc: app.state.loc.id, path: app.state.path };
  if (items.length) params.items = items.join('\n');
  const a = el('a', { href: url('/api/zip', params) });
  document.body.append(a);
  a.click();
  a.remove();
  toast('Das Archiv wird gepackt, während es lädt.', { timeout: 5000 });
}

function actionDownload() {
  const entries = selectedEntries();
  if (!entries.length) { downloadZip([]); return; }
  if (entries.length === 1 && !entries[0].dir) {
    downloadOne(joinPath(app.state.path, entries[0].name), entries[0].name);
    return;
  }
  downloadZip(entries.map((e) => e.name));
}

function setClipboard(op) {
  const items = selectedPaths();
  if (!items.length) return;
  app.state.clipboard = { op, loc: app.state.loc.id, items };
  renderContent();
  toast(`${items.length} Objekt(e) ${op === 'cut' ? 'ausgeschnitten' : 'kopiert'}. Ziel öffnen und einfügen.`, { timeout: 5000 });
}

async function actionPaste() {
  const cb = app.state.clipboard;
  if (!cb) return;
  try {
    await api.post('/api/transfer', {
      op: cb.op === 'cut' ? 'move' : 'copy',
      srcLoc: cb.loc, items: cb.items,
      dstLoc: app.state.loc.id, dstPath: app.state.path,
    });
    if (cb.op === 'cut') app.state.clipboard = null;
    toast('Übertragung gestartet.', { timeout: 2500 });
    renderContent();
  } catch (err) { showError(err, 'Einfügen fehlgeschlagen'); }
}

async function actionShare(path, entry) {
  await dialog({
    title: 'Link freigeben',
    build: ({ body, foot, close }) => {
      const daysSel = select([
        { value: '0', label: 'Unbegrenzt' },
        { value: '1', label: '1 Tag' },
        { value: '7', label: '7 Tage' },
        { value: '30', label: '30 Tage' },
      ], { value: '7' });
      const passIn = input({ type: 'password', placeholder: 'optional' });
      const out = el('div');
      body.append(
        el('p', { style: 'margin-top:2px;color:var(--fg-muted);line-height:1.6' },
          `"${entry.name}" wird über einen geheimen Link erreichbar - ohne Anmeldung. `
          + 'Der Link funktioniert nur, solange dieser Rechner erreichbar ist.'),
        field('Gültigkeit', daysSel),
        field('Passwort', passIn, 'Wer den Link öffnet, muss dann zusätzlich dieses Passwort eingeben.'),
        out,
      );
      foot.append(
        el('button', { class: 'btn', onclick: () => close() }, 'Schließen'),
        el('button', {
          class: 'btn btn-primary',
          onclick: async (ev) => {
            ev.currentTarget.disabled = true;
            try {
              const res = await api.post('/api/shares', {
                loc: app.state.loc.id, path,
                days: Number(daysSel.value), password: passIn.value,
              });
              clear(out);
              const link = res.url;
              out.append(el('div', { class: 'notice ok' },
                el('strong', {}, 'Link erstellt'),
                el('div', { style: 'margin:8px 0;word-break:break-all;font-family:var(--mono);font-size:12.5px' }, link),
                el('button', {
                  class: 'btn btn-sm',
                  onclick: async () => (await copyText(link)) ? toastOk('Kopiert.') : toast('Kopieren nicht möglich.', { type: 'warn' }),
                }, icon('copy', 15), 'Kopieren'),
              ));
            } catch (err) { showError(err, 'Link erstellen fehlgeschlagen'); }
            ev.currentTarget.disabled = false;
          },
        }, 'Link erstellen'),
      );
    },
  });
}

function showProperties(e, path) {
  dialog({
    title: 'Eigenschaften',
    build: ({ body, foot, close }) => {
      body.append(
        el('div', { style: 'display:flex;gap:14px;align-items:center;margin-bottom:14px' },
          iconFor(e),
          el('div', { style: 'min-width:0' },
            el('div', { style: 'font-weight:650;word-break:break-word' }, e.name),
            el('div', { style: 'color:var(--fg-subtle);font-size:12.5px' }, e.dir ? 'Ordner' : kindLabel(e.kind)),
          ),
        ),
        kv([
          ['Pfad', el('span', { style: 'font-family:var(--mono);font-size:12.5px;word-break:break-all' }, '/' + path)],
          ['Speicherort', app.state.loc.label],
          e.dir ? null : ['Größe', `${bytes(e.size)} (${e.size.toLocaleString('de-DE')} Bytes)`],
          ['Geändert', whenExact(e.mtime)],
        ].filter(Boolean)),
      );
      foot.append(el('button', { class: 'btn btn-primary', onclick: () => close() }, 'Schließen'));
    },
  });
}

function kindLabel(kind) {
  return {
    image: 'Bild', heic: 'Bild (HEIC)', video: 'Video', audio: 'Audio', pdf: 'PDF-Dokument',
    text: 'Textdatei', code: 'Quelltext', archive: 'Archiv', doc: 'Dokument',
  }[kind] || 'Datei';
}

// ======================================================== Hochladen ======

async function doUpload(directory) {
  const files = await pickFiles({ directory });
  if (!files.length) return;
  uploads.add(files, { loc: app.state.loc.id, path: app.state.path });
}

function wireDragDrop() {
  let depth = 0;
  let zone = null;

  const show = () => {
    if (zone || app.state.readOnly || !app.state.loc) return;
    zone = el('div', { class: 'dropzone' },
      el('div', { class: 'inner' },
        icon('upload', 42),
        el('h3', {}, 'Zum Hochladen loslassen'),
        el('p', {}, `Ziel: ${app.state.loc.label}${app.state.path ? ' / ' + app.state.path : ''}`),
      ));
    document.body.append(zone);
  };
  const hide = () => { zone?.remove(); zone = null; depth = 0; };

  window.addEventListener('dragenter', (e) => {
    if (!e.dataTransfer?.types?.includes('Files')) return;
    depth++;
    show();
  });
  window.addEventListener('dragover', (e) => {
    if (e.dataTransfer?.types?.includes('Files')) { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; }
  });
  window.addEventListener('dragleave', () => { if (--depth <= 0) hide(); });
  window.addEventListener('drop', async (e) => {
    if (!e.dataTransfer?.types?.includes('Files')) return;
    e.preventDefault();
    hide();
    if (app.state.readOnly) { toast('Dieser Speicherort ist schreibgeschützt.', { type: 'warn' }); return; }
    const files = await readDataTransfer(e.dataTransfer);
    if (!files.length) return;
    uploads.add(files, { loc: app.state.loc.id, path: app.state.path });
  });
}

// ================================================ Transfers & Aufträge ===

let jobsCache = [];
let panelCollapsed = false;

function subscribeJobs() {
  jobsStop?.();
  // Der Server sendet den Fortschritt als unbenanntes SSE-Ereignis, das
  // landet in "message".
  jobsStop = api.stream('/api/jobs/events', {
    message: (data) => {
      if (!data) return;
      jobsCache = data.jobs || [];
      renderTransfers();
      for (const j of jobsCache) {
        if (j.state === 'running' || finishedShown.has(j.id)) continue;
        finishedShown.add(j.id);
        if (j.state === 'done') {
          toastOk(`${j.op === 'move' ? 'Verschieben' : 'Kopieren'} abgeschlossen: ${j.label}`);
          refresh(true);
        } else if (j.state === 'error') {
          toast(j.error || 'Unbekannter Fehler', { type: 'error', title: 'Übertragung fehlgeschlagen' });
        }
      }
    },
    error: () => { /* EventSource verbindet von allein neu */ },
  });
}

const finishedShown = new Set();

function renderTransfers() {
  const items = uploads ? uploads.items.filter((i) => i.state !== 'fertig' || Date.now() - (i.doneAt || 0) < 4000) : [];
  const active = uploads ? uploads.items.filter((i) => ['wartet', 'läuft', 'speichert'].includes(i.state)) : [];
  const jobs = jobsCache.filter((j) => j.state === 'running' || (j.finished && Date.now() - new Date(j.finished).getTime() < 8000));

  if (!active.length && !jobs.length && !uploads?.items.some((i) => i.state === 'fehler')) {
    dom.transfers.style.display = 'none';
    return;
  }
  dom.transfers.style.display = '';
  clear(dom.transfers);

  const t = uploads?.totals() || { loaded: 0, total: 0, done: 0, count: 0 };
  const head = el('div', { class: 'panel-head' },
    el('h3', {}, active.length ? `Überträgt … (${t.done}/${t.count})` : 'Übertragungen'),
    el('button', {
      class: 'btn btn-icon', title: panelCollapsed ? 'Aufklappen' : 'Zuklappen',
      onclick: () => { panelCollapsed = !panelCollapsed; renderTransfers(); },
    }, icon(panelCollapsed ? 'chevronUp' : 'chevronDown', 17)),
    el('button', {
      class: 'btn btn-icon', title: 'Schließen',
      onclick: () => { uploads?.clearFinished(); dom.transfers.style.display = 'none'; },
    }, icon('close', 17)),
  );
  const bodyEl = el('div', { class: 'panel-body' });
  dom.transfers.className = `panel${panelCollapsed ? ' collapsed' : ''}`;
  dom.transfers.append(head, bodyEl);

  for (const it of uploads?.items || []) {
    if (it.state === 'fertig' || it.state === 'abgebrochen') continue;
    const pct = it.size ? Math.min(100, (it.loaded / it.size) * 100) : 0;
    bodyEl.append(el('div', { class: `task ${it.state === 'fehler' ? 'error' : ''}` },
      el('div', { class: 'line' },
        el('span', { class: 'title', text: baseName(it.name), title: it.name }),
        el('span', { class: 'stat' },
          it.state === 'speichert' ? 'wird gespeichert …'
            : it.state === 'wartet' ? 'wartet'
              : `${bytes(it.loaded)} / ${bytes(it.size)}`),
        el('button', {
          class: 'btn btn-icon', style: 'width:24px;height:24px', title: 'Abbrechen',
          onclick: () => uploads.cancel(it.id),
        }, icon('close', 14)),
      ),
      el('div', { class: 'bar' }, el('i', { style: `width:${it.state === 'speichert' ? 100 : pct}%` })),
      it.error ? el('div', { class: 'msg', text: it.error }) : null,
    ));
  }

  for (const j of jobs) {
    const pct = j.total ? Math.min(100, (j.done / j.total) * 100) : (j.state === 'done' ? 100 : 0);
    bodyEl.append(el('div', { class: `task ${j.state === 'done' ? 'done' : j.state === 'error' ? 'error' : ''}` },
      el('div', { class: 'line' },
        el('span', { class: 'title', text: `${j.op === 'move' ? 'Verschieben' : 'Kopieren'}: ${j.current || j.label}` }),
        el('span', { class: 'stat' },
          j.state === 'running'
            ? `${bytes(j.done)}${j.total ? ' / ' + bytes(j.total) : ''}${j.rate ? ' · ' + rate(j.rate) : ''}`
            : j.state === 'done' ? 'fertig' : j.state === 'cancelled' ? 'abgebrochen' : 'Fehler'),
        j.state === 'running' ? el('button', {
          class: 'btn btn-icon', style: 'width:24px;height:24px', title: 'Abbrechen',
          onclick: () => api.post('/api/jobs/cancel', { id: j.id }).catch(() => {}),
        }, icon('close', 14)) : null,
      ),
      el('div', { class: 'bar' }, el('i', { style: `width:${pct}%` })),
      j.error ? el('div', { class: 'msg', text: j.error }) : null,
    ));
  }
}

// ============================================================= Suche =====

let searchStop = null;

function startSearch(query) {
  searchStop?.();
  const s = app.state;
  s.search = { query, hits: [], scanned: 0, running: true };
  clear(dom.content);
  renderSearchResults();

  const u = url('/api/search', {
    loc: s.loc.id, path: s.path, q: query,
    hidden: s.showHidden ? 1 : '', limit: 500,
  });
  searchStop = api.stream(u, {
    hit: (h) => {
      if (!s.search) return;
      s.search.hits.push(h);
      if (s.search.hits.length < 400) renderSearchResults();
    },
    progress: (p) => { if (s.search) { s.search.scanned = p.scanned; updateSearchStatus(); } },
    done: (d) => {
      if (!s.search) return;
      s.search.running = false;
      s.search.scanned = d?.scanned ?? s.search.scanned;
      s.search.limited = d?.limited;
      renderSearchResults();
      searchStop?.();
      searchStop = null;
    },
    error: () => {
      if (s.search) { s.search.running = false; renderSearchResults(); }
      searchStop?.();
      searchStop = null;
    },
  });
}

function updateSearchStatus() {
  const st = $('#search-status');
  if (!st || !app.state.search) return;
  const s = app.state.search;
  st.textContent = s.running
    ? `Suche läuft … ${s.hits.length} Treffer, ${s.scanned} Ordner durchsucht`
    : `${s.hits.length} Treffer${s.limited ? ' (Begrenzung erreicht)' : ''} in ${s.scanned} Ordnern`;
}

function renderSearchResults() {
  const s = app.state.search;
  if (!s) return;
  clear(dom.content);

  const status = el('div', { class: 'search-status' },
    s.running ? el('div', { class: 'spinner' }) : icon('search', 16),
    el('span', { id: 'search-status' }),
    el('div', { style: 'flex:1' }),
    s.running ? el('button', {
      class: 'btn btn-sm',
      onclick: () => { searchStop?.(); searchStop = null; s.running = false; renderSearchResults(); },
    }, 'Stoppen') : null,
    el('button', {
      class: 'btn btn-sm',
      onclick: () => { dom.searchInput.value = ''; dom.searchBox.classList.remove('filled'); app.state.search = null; load(); },
    }, 'Zurück'),
  );
  dom.content.append(status);
  updateSearchStatus();

  if (!s.hits.length) {
    dom.content.append(s.running
      ? el('div', { style: 'padding:40px;text-align:center;color:var(--fg-muted)' }, 'Wird durchsucht …')
      : emptyState('search', 'Nichts gefunden', `Kein Treffer für "${s.query}".`));
    return;
  }

  const rows = el('div', { class: 'rows' });
  for (const h of s.hits) {
    rows.append(el('div', {
      class: 'row-item',
      ondblclick: () => openHit(h),
      onclick: (e) => { if (isTouch) openHit(h); },
      oncontextmenu: (e) => {
        e.preventDefault();
        menu(e.clientX, e.clientY, [
          { label: 'Im Ordner zeigen', icon: 'folder', onClick: () => { app.state.search = null; dom.searchInput.value = ''; navigate(app.state.loc.id, h.dir); } },
          { label: h.isDir ? 'Öffnen' : 'Herunterladen', icon: h.isDir ? 'folder' : 'download', onClick: () => openHit(h) },
        ]);
      },
    },
      el('div'),
      el('div', { class: 'cell-name' },
        h.isDir || !h.thumb || !app.state.thumbs
          ? el('div', { class: `fico ${h.isDir ? 'folder' : h.kind}` }, icon(kindIcon(h.isDir ? 'folder' : h.kind)))
          : (() => {
            const box = el('div', { class: 'thumb-box' });
            const img = el('img', { loading: 'lazy', alt: '', src: url('/api/thumb', { loc: app.state.loc.id, path: h.path, w: 160 }) });
            img.addEventListener('error', () => box.replaceWith(el('div', { class: `fico ${h.kind}` }, icon(kindIcon(h.kind)))));
            box.append(img);
            return box;
          })(),
        el('div', { style: 'min-width:0' },
          el('div', { class: 'name', text: h.name }),
          el('div', { class: 'hit-path', text: '/' + (h.dir || '') }),
        ),
      ),
      el('div', { class: 'cell-size', text: h.isDir ? '' : bytes(h.size) }),
      el('div', { class: 'cell-date', text: when(h.mtime) }),
      el('div'),
    ));
  }
  dom.content.append(rows);
}

function openHit(h) {
  if (h.isDir) { app.state.search = null; dom.searchInput.value = ''; navigate(app.state.loc.id, h.path); return; }
  const entry = { name: h.name, size: h.size, dir: false, mtime: h.mtime, kind: h.kind, thumb: h.thumb };
  if (canView(entry)) {
    openViewer({ loc: app.state.loc.id, dir: h.dir, entries: [entry], index: 0, readOnly: app.state.readOnly });
    return;
  }
  const a = el('a', { href: url('/api/download', { loc: app.state.loc.id, path: h.path }), download: h.name });
  document.body.append(a); a.click(); a.remove();
}

// ========================================================= Tastatur ======

function wireKeyboard() {
  document.addEventListener('keydown', (e) => {
    const tag = document.activeElement?.tagName;
    const typing = tag === 'INPUT' || tag === 'TEXTAREA' || document.activeElement?.isContentEditable;
    const mod = e.ctrlKey || e.metaKey;

    if (mod && e.key.toLowerCase() === 'f') { e.preventDefault(); dom.searchInput.focus(); dom.searchInput.select(); return; }
    if (typing) return;
    if (document.querySelector('.overlay, .viewer')) return;

    switch (true) {
      case mod && e.key.toLowerCase() === 'a':
        e.preventDefault(); selectAll(); break;
      case mod && e.key.toLowerCase() === 'c':
        if (app.state.selection.size) { e.preventDefault(); setClipboard('copy'); } break;
      case mod && e.key.toLowerCase() === 'x':
        if (app.state.selection.size && !app.state.readOnly) { e.preventDefault(); setClipboard('cut'); } break;
      case mod && e.key.toLowerCase() === 'v':
        if (app.state.clipboard && !app.state.readOnly) { e.preventDefault(); actionPaste(); } break;
      case e.key === 'Delete':
        if (app.state.selection.size && !app.state.readOnly) { e.preventDefault(); actionDelete(); } break;
      case e.key === 'F2':
        if (app.state.selection.size === 1 && !app.state.readOnly) { e.preventDefault(); actionRename(); } break;
      case e.key === 'F5':
        e.preventDefault(); refresh(); break;
      case e.key === 'Backspace':
        e.preventDefault(); goUp(); break;
      case e.key === 'Escape':
        closeMenu();
        if (app.state.selection.size) { app.state.selection.clear(); renderContent(); }
        break;
      case e.key === 'Enter': {
        const first = selectedEntries()[0];
        if (first) { e.preventDefault(); openEntry(first, app.state.entries.indexOf(first)); }
        break;
      }
      case e.key === '?':
        openSettings(app, 'about'); break;
      case e.key === 'ArrowDown' || e.key === 'ArrowUp': {
        e.preventDefault();
        moveSelection(e.key === 'ArrowDown' ? 1 : -1, e.shiftKey);
        break;
      }
    }
  });
}

function moveSelection(dir, extend) {
  const s = app.state;
  if (!s.entries.length) return;
  let idx = s.anchor >= 0 ? s.anchor + dir : (dir > 0 ? 0 : s.entries.length - 1);
  idx = Math.max(0, Math.min(s.entries.length - 1, idx));
  if (!extend) s.selection.clear();
  s.selection.add(s.entries[idx].name);
  s.anchor = idx;
  renderContent();
  const node = dom.content.querySelector(`[data-index="${idx}"]`);
  node?.scrollIntoView({ block: 'nearest' });
}

// ==================================================== Service Worker =====

function registerServiceWorker() {
  if (!('serviceWorker' in navigator)) return;
  // Nur über HTTPS oder localhost erlaubt - im Heimnetz per IP und HTTP
  // gibt es die Registrierung schlicht nicht, das ist kein Fehler.
  navigator.serviceWorker.register('/sw.js').catch(() => {});
}

window.app = app;
