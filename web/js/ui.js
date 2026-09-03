// Wiederverwendbare Bausteine der Oberfläche: Meldungen, Dialoge, Menüs.

import { el, clear, icon, $ } from './util.js';

// ------------------------------------------------------------- Toasts ----

let toastHost = null;
function host() {
  if (!toastHost) {
    toastHost = el('div', { class: 'toasts', role: 'status', 'aria-live': 'polite' });
    document.body.appendChild(toastHost);
  }
  return toastHost;
}

/**
 * Kurze Rückmeldung am unteren Rand.
 * @param {string} msg    Text
 * @param {object} opts   {type: 'info'|'ok'|'error'|'warn', title, timeout, action:{label,fn}}
 */
export function toast(msg, opts = {}) {
  const { type = 'info', title = '', timeout = type === 'error' ? 9000 : 4000, action } = opts;
  const node = el('div', { class: `toast ${type}` },
    el('div', { class: 'body' },
      title ? el('strong', { text: title }) : null,
      msg,
      action ? el('div', { style: 'margin-top:6px' },
        el('button', { class: 'btn btn-sm', onclick: () => { action.fn(); dismiss(); } }, action.label)
      ) : null,
    ),
    el('button', { class: 'close', title: 'Schließen', onclick: () => dismiss() }, icon('close', 15)),
  );
  host().appendChild(node);

  let timer = timeout ? setTimeout(dismiss, timeout) : null;
  function dismiss() {
    if (timer) clearTimeout(timer);
    if (!node.isConnected) return;
    node.classList.add('out');
    setTimeout(() => node.remove(), 200);
  }
  node.addEventListener('mouseenter', () => { if (timer) { clearTimeout(timer); timer = null; } });
  return dismiss;
}

export const toastOk = (m, o) => toast(m, { ...o, type: 'ok' });
export const toastErr = (m, o) => toast(m, { ...o, type: 'error' });

/** Zeigt einen Fehler verständlich an; Details stehen in der Konsole. */
export function showError(err, context = '') {
  console.error(context, err);
  const msg = err?.message || String(err);
  toast(msg, { type: 'error', title: context || undefined });
}

// ------------------------------------------------------------- Dialoge ---

let openDialogs = 0;

/**
 * Modaler Dialog. build(api) baut den Inhalt und bekommt {close, foot, body}.
 * Liefert ein Promise mit dem Wert, mit dem close() aufgerufen wurde.
 */
export function dialog({ title, wide = false, build, onClose }) {
  return new Promise((resolve) => {
    const body = el('div', { class: 'dialog-body' });
    const foot = el('div', { class: 'dialog-foot' });
    const box = el('div', { class: `dialog${wide ? ' wide' : ''}`, role: 'dialog', 'aria-modal': 'true' },
      el('div', { class: 'dialog-head' },
        el('h2', { text: title }),
        el('button', { class: 'btn btn-icon', title: 'Schließen', onclick: () => close(undefined) }, icon('close', 18)),
      ),
      body, foot,
    );
    const overlay = el('div', { class: 'overlay' }, box);

    let done = false;
    function close(value) {
      if (done) return;
      done = true;
      overlay.remove();
      openDialogs--;
      document.removeEventListener('keydown', onKey, true);
      if (openDialogs === 0) document.body.style.overflow = '';
      onClose?.(value);
      resolve(value);
    }
    function onKey(e) {
      if (e.key === 'Escape') { e.stopPropagation(); close(undefined); }
    }
    overlay.addEventListener('mousedown', (e) => { if (e.target === overlay) close(undefined); });
    document.addEventListener('keydown', onKey, true);

    build({ body, foot, close, box });

    document.body.appendChild(overlay);
    openDialogs++;
    document.body.style.overflow = 'hidden';

    // Erstes sinnvolles Element fokussieren, damit die Tastatur sofort greift.
    requestAnimationFrame(() => {
      const first = box.querySelector('input:not([type=hidden]), textarea, select, button.btn-primary');
      first?.focus();
      if (first?.select && first.dataset.selectAll !== 'false') first.select?.();
    });
  });
}

/** Rückfrage mit Ja/Nein. */
export function confirm({ title, message, confirmLabel = 'Bestätigen', danger = false, detail }) {
  return dialog({
    title,
    build: ({ body, foot, close }) => {
      body.append(el('p', { style: 'margin:2px 0 12px;line-height:1.6' }, message));
      if (detail) body.append(el('div', { class: 'notice warn', text: detail }));
      foot.append(
        el('button', { class: 'btn', onclick: () => close(false) }, 'Abbrechen'),
        el('button', {
          class: `btn ${danger ? 'btn-danger' : 'btn-primary'}`,
          onclick: () => close(true),
        }, confirmLabel),
      );
    },
  });
}

/** Einzeilige Eingabe. */
export function prompt({ title, label, value = '', placeholder = '', confirmLabel = 'OK', hint, selectStem = false }) {
  return dialog({
    title,
    build: ({ body, foot, close }) => {
      const input = el('input', { class: 'input', value, placeholder, autocomplete: 'off' });
      const form = el('form', { onsubmit: (e) => { e.preventDefault(); submit(); } },
        el('div', { class: 'field' },
          label ? el('label', { text: label }) : null,
          input,
          hint ? el('div', { class: 'hint', text: hint }) : null,
        ),
      );
      const submit = () => {
        const v = input.value.trim();
        if (v) close(v);
      };
      body.append(form);
      foot.append(
        el('button', { class: 'btn', onclick: () => close(undefined) }, 'Abbrechen'),
        el('button', { class: 'btn btn-primary', onclick: submit }, confirmLabel),
      );
      requestAnimationFrame(() => {
        input.focus();
        // Beim Umbenennen nur den Namen ohne Endung markieren - so wie im
        // Explorer und im Finder.
        const dot = value.lastIndexOf('.');
        if (selectStem && dot > 0) input.setSelectionRange(0, dot);
        else input.select();
      });
    },
  });
}

// ------------------------------------------------------------- Menüs ----

let activeMenu = null;

/** Schließt ein offenes Kontextmenü. */
export function closeMenu() {
  if (activeMenu) { activeMenu.remove(); activeMenu = null; }
}

/**
 * Kontextmenü an einer Bildschirmposition.
 * items: [{label, icon, onClick, danger, disabled, shortcut}] oder 'separator'
 */
export function menu(x, y, items) {
  closeMenu();
  const node = el('div', { class: 'menu', role: 'menu' });
  for (const it of items) {
    if (!it) continue;
    if (it === 'separator') { node.append(el('hr')); continue; }
    node.append(el('button', {
      class: it.danger ? 'danger' : '',
      role: 'menuitem',
      disabled: it.disabled || false,
      onclick: (e) => { e.stopPropagation(); closeMenu(); it.onClick?.(); },
    },
      it.icon ? icon(it.icon, 16) : el('span', { style: 'width:16px' }),
      el('span', { text: it.label }),
      it.shortcut ? el('span', { class: 'shortcut', text: it.shortcut }) : null,
    ));
  }
  document.body.appendChild(node);
  activeMenu = node;

  // Innerhalb des Fensters halten.
  const r = node.getBoundingClientRect();
  const pad = 8;
  let left = x, top = y;
  if (left + r.width > innerWidth - pad) left = Math.max(pad, innerWidth - r.width - pad);
  if (top + r.height > innerHeight - pad) top = Math.max(pad, innerHeight - r.height - pad);
  node.style.left = `${left}px`;
  node.style.top = `${top}px`;

  const off = () => { closeMenu(); document.removeEventListener('mousedown', off); document.removeEventListener('scroll', off, true); };
  setTimeout(() => {
    document.addEventListener('mousedown', off);
    document.addEventListener('scroll', off, true);
  }, 0);
  return node;
}

// ------------------------------------------------------ Formularhelfer ---

/** Beschriftetes Textfeld. */
export function field(label, input, hint) {
  return el('div', { class: 'field' },
    label ? el('label', { text: label }) : null,
    input,
    hint ? el('div', { class: 'hint', html: hint }) : null,
  );
}

export function input(attrs = {}) {
  return el('input', { class: 'input', autocomplete: 'off', ...attrs });
}

export function select(options, attrs = {}) {
  const s = el('select', { class: 'select', ...attrs });
  for (const o of options) {
    s.append(el('option', { value: o.value, selected: o.value === attrs.value }, o.label));
  }
  return s;
}

export function checkbox(label, checked, onChange) {
  const box = el('input', { type: 'checkbox', checked: checked || false });
  box.addEventListener('change', () => onChange?.(box.checked));
  const wrap = el('label', { class: 'check' }, box, el('span', { text: label }));
  wrap.input = box;
  return wrap;
}

/** Anzeigeblock für Schlüssel/Wert-Paare. */
export function kv(pairs) {
  const dl = el('dl', { class: 'kv' });
  for (const [k, v] of pairs) {
    if (v === null || v === undefined || v === '') continue;
    dl.append(el('dt', { text: k }), el('dd', {}, v));
  }
  return dl;
}

/** Ladeanzeige mit Text. */
export function busy(text = 'Einen Moment …') {
  return el('div', { style: 'display:flex;align-items:center;gap:10px;padding:16px 2px;color:var(--fg-muted)' },
    el('div', { class: 'spinner' }), text);
}

/** Leerer Zustand mit Symbol und Erklärung. */
export function emptyState(iconName, title, text, action) {
  return el('div', { class: 'empty' },
    icon(iconName, 46),
    el('h3', { text: title }),
    text ? el('p', { text }) : null,
    action || null,
  );
}
