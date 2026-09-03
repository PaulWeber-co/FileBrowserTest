// Vollbildansicht für Bilder, Videos, Ton, PDF und Text.

import { el, clear, icon, bytes, when, url, baseName } from './util.js';
import * as api from './api.js';
import { toast, toastOk, showError, confirm } from './ui.js';

const VIEWABLE = new Set(['image', 'heic', 'video', 'audio', 'pdf', 'text', 'code']);

/** Meldet, ob eine Datei im Viewer angezeigt werden kann. */
export function canView(entry) {
  return !entry.dir && VIEWABLE.has(entry.kind);
}

let current = null;

/**
 * Oeffnet den Viewer.
 * @param {object} ctx {loc, dir, entries, index, readOnly, onChanged}
 */
export function openViewer(ctx) {
  closeViewer();

  const state = {
    ...ctx,
    items: ctx.entries.filter(canView),
    idx: 0,
    editing: false,
  };
  const startName = ctx.entries[ctx.index]?.name;
  state.idx = Math.max(0, state.items.findIndex((e) => e.name === startName));

  const stage = el('div', { class: 'viewer-stage' });
  const titleEl = el('div', { class: 'title' });
  const actions = el('div', { style: 'display:flex;gap:2px;align-items:center' });
  const foot = el('div', { class: 'viewer-foot' });

  const root = el('div', { class: 'viewer', role: 'dialog', 'aria-modal': 'true' },
    el('div', { class: 'viewer-head' },
      el('button', { class: 'btn btn-icon', title: 'Schließen (Esc)', onclick: () => dismiss() }, icon('close', 20)),
      titleEl,
      actions,
    ),
    stage,
    foot,
  );

  if (state.items.length > 1) {
    root.append(
      el('button', { class: 'viewer-nav prev', title: 'Vorheriges', onclick: () => step(-1) }, icon('chevronLeft', 22)),
      el('button', { class: 'viewer-nav next', title: 'Nächstes', onclick: () => step(1) }, icon('chevronRight', 22)),
    );
  }

  document.body.appendChild(root);
  document.body.style.overflow = 'hidden';
  document.addEventListener('keydown', onKey, true);
  attachSwipe(stage, step);

  current = { root, close: dismiss };
  render();

  // Bewusst nicht "close": eine gleichnamige lokale Funktion würde die
  // exportierte überschatten - und der Aufruf ganz oben liefe dann in die
  // temporale Todeszone von "root".
  function dismiss() {
    document.removeEventListener('keydown', onKey, true);
    root.remove();
    document.body.style.overflow = '';
    current = null;
  }

  function onKey(e) {
    if (state.editing && e.key !== 'Escape') return;
    switch (e.key) {
      case 'Escape': e.preventDefault(); dismiss(); break;
      case 'ArrowLeft': step(-1); break;
      case 'ArrowRight': step(1); break;
      case ' ': {
        const v = stage.querySelector('video, audio');
        if (v) { e.preventDefault(); v.paused ? v.play() : v.pause(); }
        break;
      }
    }
  }

  function step(dir) {
    if (state.items.length < 2) return;
    state.idx = (state.idx + dir + state.items.length) % state.items.length;
    render();
  }

  function fileURL(entry, download = false) {
    const path = state.dir ? `${state.dir}/${entry.name}` : entry.name;
    return url(download ? '/api/download' : '/api/raw', { loc: state.loc, path });
  }

  function render() {
    const entry = state.items[state.idx];
    if (!entry) { dismiss(); return; }
    state.editing = false;
    clear(stage);
    clear(actions);
    clear(titleEl);

    titleEl.append(
      el('div', { text: entry.name }),
      el('div', { class: 'sub', text: `${bytes(entry.size)} · ${when(entry.mtime)}` }),
    );

    actions.append(
      el('a', {
        class: 'btn btn-icon', title: 'Herunterladen',
        href: fileURL(entry, true), download: entry.name,
      }, icon('download', 19)),
    );

    const path = state.dir ? `${state.dir}/${entry.name}` : entry.name;

    switch (entry.kind) {
      case 'image':
      case 'heic':
        renderImage(entry);
        break;
      case 'video':
        stage.append(el('video', {
          src: fileURL(entry), controls: true, autoplay: true, playsinline: true,
          preload: 'metadata', style: 'max-width:100%;max-height:100%',
        }));
        break;
      case 'audio':
        stage.append(el('div', { class: 'audio-wrap' },
          el('div', { class: 'fico audio' }, icon('audio')),
          el('div', { style: 'font-weight:600' }, entry.name),
          el('audio', { src: fileURL(entry), controls: true, autoplay: true, style: 'width:min(460px,80vw)' }),
        ));
        break;
      case 'pdf':
        renderPDF(entry);
        break;
      case 'text':
      case 'code':
        renderText(entry, path);
        break;
    }

    foot.textContent = state.items.length > 1
      ? `${state.idx + 1} von ${state.items.length}`
      : '';
  }

  function renderImage(entry) {
    const img = el('img', { src: fileURL(entry), alt: entry.name, decoding: 'async' });
    let zoomed = false;
    img.addEventListener('click', () => {
      zoomed = !zoomed;
      img.classList.toggle('zoomed', zoomed);
      img.style.transform = zoomed ? 'scale(2)' : '';
      img.style.maxWidth = zoomed ? 'none' : '';
      img.style.maxHeight = zoomed ? 'none' : '';
      stage.style.overflow = zoomed ? 'auto' : 'hidden';
    });
    img.addEventListener('error', () => {
      clear(stage);
      stage.append(el('div', { style: 'color:#fff;text-align:center;padding:30px' },
        el('p', {}, 'Dieses Bild kann der Browser nicht darstellen.'),
        el('a', { class: 'btn', href: fileURL(entry, true), download: entry.name }, 'Herunterladen'),
      ));
    });
    stage.append(img);
  }

  function renderPDF(entry) {
    const src = fileURL(entry);
    stage.append(el('iframe', { src, title: entry.name }));
    actions.prepend(el('a', {
      class: 'btn btn-icon', title: 'In neuem Tab öffnen',
      href: src, target: '_blank', rel: 'noopener',
    }, icon('eye', 19)));
  }

  async function renderText(entry, path) {
    stage.append(el('pre', { text: 'Wird geladen …' }));
    let data;
    try {
      data = await api.get(url('/api/text', { loc: state.loc, path }));
    } catch (err) {
      clear(stage);
      stage.append(el('pre', { text: `Konnte nicht geladen werden:\n${err.message}` }));
      return;
    }
    clear(stage);
    const pre = el('pre', { text: data.content });
    stage.append(pre);

    if (state.readOnly) return;

    const editBtn = el('button', { class: 'btn btn-icon', title: 'Bearbeiten' }, icon('edit', 19));
    actions.prepend(editBtn);
    editBtn.addEventListener('click', () => {
      state.editing = true;
      clear(stage);
      const ta = el('textarea', { spellcheck: 'false' });
      ta.value = data.content;
      stage.append(ta);
      clear(actions);

      const save = el('button', { class: 'btn btn-primary btn-sm' }, 'Speichern');
      const cancel = el('button', { class: 'btn btn-sm' }, 'Abbrechen');
      actions.append(cancel, save);

      cancel.addEventListener('click', () => { state.editing = false; render(); });
      save.addEventListener('click', async () => {
        save.disabled = true;
        try {
          await api.post('/api/text', { loc: state.loc, path, content: ta.value });
          data.content = ta.value;
          toastOk('Gespeichert.');
          state.editing = false;
          state.onChanged?.();
          render();
        } catch (err) {
          showError(err, 'Speichern fehlgeschlagen');
          save.disabled = false;
        }
      });
      ta.focus();
    });
  }
}

/** Schließt einen offenen Viewer. */
export function closeViewer() {
  current?.close();
}

/** Wischgesten für das Blättern auf dem Handy. */
function attachSwipe(node, step) {
  let x0 = null, y0 = null, t0 = 0;
  node.addEventListener('touchstart', (e) => {
    if (e.touches.length !== 1) { x0 = null; return; }
    x0 = e.touches[0].clientX;
    y0 = e.touches[0].clientY;
    t0 = Date.now();
  }, { passive: true });
  node.addEventListener('touchend', (e) => {
    if (x0 === null) return;
    const t = e.changedTouches[0];
    const dx = t.clientX - x0, dy = t.clientY - y0;
    // Nur waagerechte, zügige Gesten zählen - sonst kollidiert es mit
    // dem Zoomen und dem Scrollen.
    if (Math.abs(dx) > 60 && Math.abs(dx) > Math.abs(dy) * 1.8 && Date.now() - t0 < 700) {
      step(dx < 0 ? 1 : -1);
    }
    x0 = null;
  }, { passive: true });
}
