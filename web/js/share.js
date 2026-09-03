// Oeffentliche Ansicht eines Freigabelinks - bewusst schlank gehalten.

import { el, clear, icon, kindIcon, bytes, when, url, joinPath } from './util.js';
import * as api from './api.js';
import { toast, showError } from './ui.js';

const token = location.pathname.split('/')[2] || '';
const body = document.getElementById('body');
const zipBtn = document.getElementById('zip-btn');

let current = { path: '', isDir: false, name: '' };

const t = localStorage.getItem('theme');
if (t && t !== 'auto') document.documentElement.setAttribute('data-theme', t);

zipBtn.addEventListener('click', () => {
  location.href = url(`/s/${token}/zip`, { path: current.path });
});

load('');

async function load(path) {
  clear(body);
  body.append(el('div', { style: 'display:flex;gap:10px;align-items:center;padding:24px;color:var(--fg-muted)' },
    el('div', { class: 'spinner' }), 'Wird geladen …'));
  try {
    const data = await api.get(url(`/s/${token}/list`, { path }));
    current = { path: data.path || '', isDir: data.isDir, name: data.name };
    render(data);
  } catch (err) {
    if (err.status === 401) { renderUnlock(); return; }
    clear(body);
    body.append(el('div', { class: 'notice error', text: err.message }));
  }
}

function renderUnlock() {
  clear(body);
  const pass = el('input', { class: 'input', type: 'password', placeholder: 'Passwort', autofocus: true });
  const err = el('div', { class: 'notice error', hidden: true });
  const form = el('form', {
    style: 'max-width:340px;margin:60px auto',
    onsubmit: async (e) => {
      e.preventDefault();
      err.hidden = true;
      try {
        await api.post(`/s/${token}/unlock`, { password: pass.value });
        load('');
      } catch (ex) { err.textContent = ex.message; err.hidden = false; }
    },
  },
    el('h2', { style: 'margin:0 0 6px;font-size:18px' }, 'Passwort erforderlich'),
    el('p', { style: 'margin:0 0 16px;color:var(--fg-muted)' }, 'Diese Freigabe ist geschützt.'),
    err,
    el('div', { class: 'field' }, pass),
    el('button', { class: 'btn btn-primary', style: 'width:100%', type: 'submit' }, 'Öffnen'),
  );
  body.append(form);
}

function render(data) {
  clear(body);
  zipBtn.hidden = !data.isDir;

  const crumbs = el('div', { class: 'crumbs', style: 'margin-bottom:12px' },
    el('button', { class: 'crumb', onclick: () => load('') }, data.name),
  );
  for (const c of data.crumbs || []) {
    crumbs.append(el('span', { class: 'crumb-sep' }, icon('chevronRight', 14)));
    crumbs.append(el('button', { class: 'crumb', onclick: () => load(c.path) }, c.name));
  }
  if (data.isDir) body.append(crumbs);

  if (!data.entries?.length) {
    body.append(el('div', { class: 'empty' }, icon('folder', 46), el('h3', {}, 'Dieser Ordner ist leer')));
    return;
  }

  const rows = el('div', { class: 'rows' });
  for (const e of data.entries) {
    const path = data.isDir ? joinPath(data.path, e.name) : '';
    rows.append(el('div', {
      class: 'row-item', style: 'cursor:pointer',
      onclick: () => {
        if (e.dir) { load(path); return; }
        location.href = url(`/s/${token}/dl`, { path, dl: 1 });
      },
    },
      el('div'),
      el('div', { class: 'cell-name' },
        e.thumb && !e.dir
          ? (() => {
            const box = el('div', { class: 'thumb-box' });
            const img = el('img', { loading: 'lazy', alt: '', src: url(`/s/${token}/thumb`, { path }) });
            img.addEventListener('error', () => box.replaceWith(el('div', { class: `fico ${e.kind}` }, icon(kindIcon(e.kind)))));
            box.append(img);
            return box;
          })()
          : el('div', { class: `fico ${e.dir ? 'folder' : e.kind}` }, icon(kindIcon(e.dir ? 'folder' : e.kind))),
        el('span', { class: 'name', text: e.name }),
      ),
      el('div', { class: 'cell-size', text: e.dir ? '' : bytes(e.size) }),
      el('div', { class: 'cell-date', text: when(e.mtime) }),
      el('div', { class: 'cell-menu' },
        e.dir ? null : el('a', {
          class: 'btn btn-icon', title: 'Herunterladen',
          href: url(`/s/${token}/dl`, { path, dl: 1 }),
          onclick: (ev) => ev.stopPropagation(),
        }, icon('download', 17)),
      ),
    ));
  }
  body.append(rows);
}
