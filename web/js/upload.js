// Hochladen mit Warteschlange, Teilen und Wiederaufnahme.
//
// Große Dateien gehen in Stücken zum Server, der sie erst zwischenspeichert
// und dann am Stück auf den Netzwerkspeicher schreibt. Das entkoppelt die
// schnelle WLAN-Strecke vom langsamen USB-Anschluss am Router und macht einen
// abgebrochenen Upload wiederaufnehmbar statt zur halben Datei.

import * as api from './api.js';

const DIRECT_LIMIT = 8 * 1024 * 1024;   // darunter lohnt die Stückelung nicht
const PART_RETRIES = 3;
const PARALLEL_FILES = 2;
const PARALLEL_PARTS = 2;

let nextId = 1;

export class UploadQueue {
  constructor({ onUpdate, onFileDone } = {}) {
    this.items = [];
    this.onUpdate = onUpdate || (() => {});
    this.onFileDone = onFileDone || (() => {});
    this.running = 0;
    this.partSize = 8 * 1024 * 1024;
  }

  /**
   * Dateien einreihen.
   * @param {Array<{file: File, relPath?: string}>} files
   * @param {{loc: string, path: string, mode?: string}} target
   */
  add(files, target) {
    for (const f of files) {
      this.items.push({
        id: nextId++,
        file: f.file,
        name: f.relPath || f.file.name,
        loc: target.loc,
        path: target.path,
        mode: target.mode || 'rename',
        size: f.file.size,
        loaded: 0,
        state: 'wartet',         // wartet | läuft | speichert | fertig | fehler | abgebrochen
        error: '',
        uploadId: null,
        abort: new AbortController(),
      });
    }
    this.onUpdate();
    this.pump();
  }

  get active() {
    return this.items.filter((i) => i.state === 'läuft' || i.state === 'speichert' || i.state === 'wartet');
  }

  totals() {
    let total = 0, loaded = 0, done = 0, failed = 0;
    for (const i of this.items) {
      total += i.size;
      loaded += i.loaded;
      if (i.state === 'fertig') done++;
      if (i.state === 'fehler') failed++;
    }
    return { total, loaded, done, failed, count: this.items.length };
  }

  cancel(id) {
    const it = this.items.find((i) => i.id === id);
    if (!it) return;
    it.abort.abort();
    it.state = 'abgebrochen';
    if (it.uploadId) api.post('/api/upload/abort', { id: it.uploadId }).catch(() => {});
    this.onUpdate();
    this.pump();
  }

  cancelAll() {
    for (const i of this.items) {
      if (i.state === 'wartet' || i.state === 'läuft' || i.state === 'speichert') this.cancel(i.id);
    }
  }

  clearFinished() {
    this.items = this.items.filter((i) => i.state === 'läuft' || i.state === 'speichert' || i.state === 'wartet');
    this.onUpdate();
  }

  pump() {
    while (this.running < PARALLEL_FILES) {
      const next = this.items.find((i) => i.state === 'wartet');
      if (!next) break;
      this.running++;
      this.run(next).finally(() => { this.running--; this.pump(); });
    }
  }

  async run(it) {
    it.state = 'läuft';
    this.onUpdate();
    try {
      if (it.size <= DIRECT_LIMIT) await this.direct(it);
      else await this.chunked(it);
      it.state = 'fertig';
      it.loaded = it.size;
      this.onFileDone(it);
    } catch (err) {
      if (err?.name === 'AbortError' || it.state === 'abgebrochen') {
        it.state = 'abgebrochen';
      } else {
        it.state = 'fehler';
        it.error = err?.message || String(err);
      }
    }
    this.onUpdate();
  }

  async direct(it) {
    const u = `/api/upload?loc=${encodeURIComponent(it.loc)}&path=${encodeURIComponent(it.path)}`
      + `&name=${encodeURIComponent(it.name)}&mode=${encodeURIComponent(it.mode)}`;
    await api.sendRaw(u, it.file, {
      signal: it.abort.signal,
      onProgress: (loaded) => { it.loaded = loaded; this.onUpdate(); },
    });
  }

  async chunked(it) {
    const init = await api.post('/api/upload/init', {
      loc: it.loc, path: it.path, name: it.name, size: it.size, mode: it.mode,
    }, { signal: it.abort.signal });
    it.uploadId = init.uploadId;
    const partSize = Math.max(1 << 20, init.partSize || this.partSize);

    // Alle Teilbereiche vorbereiten.
    const parts = [];
    for (let off = 0; off < it.size; off += partSize) {
      parts.push({ off, len: Math.min(partSize, it.size - off) });
    }

    const progress = new Map();
    const report = () => {
      let sum = 0;
      for (const v of progress.values()) sum += v;
      it.loaded = sum;
      this.onUpdate();
    };

    let cursor = 0;
    const worker = async () => {
      while (cursor < parts.length) {
        const p = parts[cursor++];
        await this.sendPart(it, p, progress, report);
      }
    };
    const workers = [];
    for (let i = 0; i < Math.min(PARALLEL_PARTS, parts.length); i++) workers.push(worker());
    await Promise.all(workers);

    it.state = 'speichert';
    this.onUpdate();

    try {
      await api.post('/api/upload/finish', { id: it.uploadId }, { signal: it.abort.signal });
    } catch (err) {
      // Fehlende Teile nachreichen und noch einmal abschließen.
      if (err.missing && err.missing.length) {
        for (const [off, len] of err.missing) {
          await this.sendPart(it, { off, len }, progress, report);
        }
        await api.post('/api/upload/finish', { id: it.uploadId }, { signal: it.abort.signal });
      } else {
        throw err;
      }
    }
    it.uploadId = null;
  }

  async sendPart(it, p, progress, report) {
    const blob = it.file.slice(p.off, p.off + p.len);
    const u = `/api/upload/part?id=${encodeURIComponent(it.uploadId)}&offset=${p.off}`;
    let lastErr;
    for (let attempt = 0; attempt < PART_RETRIES; attempt++) {
      try {
        await api.sendRaw(u, blob, {
          signal: it.abort.signal,
          onProgress: (loaded) => { progress.set(p.off, loaded); report(); },
        });
        progress.set(p.off, p.len);
        report();
        return;
      } catch (err) {
        if (err?.name === 'AbortError') throw err;
        lastErr = err;
        progress.set(p.off, 0);
        report();
        // Kurz warten und erneut versuchen - WLAN-Aussetzer sind häufig.
        await new Promise((r) => setTimeout(r, 400 * (attempt + 1)));
      }
    }
    throw lastErr;
  }
}

/**
 * Liest einen Drag-and-drop-Vorgang aus - auch ganze Ordnerbäume.
 * Liefert [{file, relPath}].
 */
export async function readDataTransfer(dt) {
  const out = [];
  const items = dt.items ? Array.from(dt.items) : [];
  const entries = items
    .map((i) => (i.webkitGetAsEntry ? i.webkitGetAsEntry() : null))
    .filter(Boolean);

  if (entries.length) {
    await Promise.all(entries.map((e) => walkEntry(e, '', out)));
    if (out.length) return out;
  }
  for (const f of Array.from(dt.files || [])) out.push({ file: f, relPath: f.name });
  return out;
}

function walkEntry(entry, prefix, out) {
  return new Promise((resolve) => {
    if (entry.isFile) {
      entry.file(
        (file) => { out.push({ file, relPath: prefix + entry.name }); resolve(); },
        () => resolve(),
      );
      return;
    }
    if (!entry.isDirectory) { resolve(); return; }
    const reader = entry.createReader();
    const all = [];
    const readBatch = () => {
      // readEntries liefert maximal 100 Einträge pro Aufruf - deshalb die
      // Schleife, sonst fehlen bei großen Ordnern Dateien.
      reader.readEntries(async (batch) => {
        if (!batch.length) {
          await Promise.all(all.map((e) => walkEntry(e, `${prefix + entry.name}/`, out)));
          resolve();
          return;
        }
        all.push(...batch);
        readBatch();
      }, () => resolve());
    };
    readBatch();
  });
}

/** Dateiauswahl über einen versteckten Datei-Dialog. */
export function pickFiles({ directory = false } = {}) {
  return new Promise((resolve) => {
    const inp = document.createElement('input');
    inp.type = 'file';
    inp.multiple = true;
    if (directory) { inp.webkitdirectory = true; inp.directory = true; }
    inp.style.cssText = 'position:fixed;left:-9999px';
    document.body.appendChild(inp);
    inp.addEventListener('change', () => {
      const files = Array.from(inp.files || []).map((f) => ({
        file: f,
        relPath: f.webkitRelativePath || f.name,
      }));
      inp.remove();
      resolve(files);
    }, { once: true });
    // Wird der Dialog abgebrochen, feuert 'change' nie - der Knoten wird
    // beim nächsten Fokus aufgeräumt.
    window.addEventListener('focus', () => setTimeout(() => { if (inp.isConnected && !inp.files?.length) { inp.remove(); resolve([]); } }, 500), { once: true });
    inp.click();
  });
}
