// Dünne Hülle um fetch: einheitliche Fehler, CSRF-Header, Abbruch.

/** Fehler mit HTTP-Status und Servermeldung. */
export class ApiError extends Error {
  constructor(message, status, detail, code) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.detail = detail;
    this.code = code;
  }
}

const HEADERS = { 'X-SpeedNAS': '1' };

async function handle(res) {
  if (res.status === 401 && !location.pathname.startsWith('/login')) {
    location.href = '/login';
    throw new ApiError('Sitzung abgelaufen.', 401);
  }
  const ct = res.headers.get('content-type') || '';
  if (!res.ok) {
    let msg = `Fehler ${res.status}`, detail = '', code = '';
    if (ct.includes('json')) {
      try {
        const j = await res.json();
        msg = j.error || msg; detail = j.detail || ''; code = j.code || '';
        if (j.missing) { const e = new ApiError(msg, res.status, detail, code); e.missing = j.missing; throw e; }
      } catch (e) { if (e instanceof ApiError) throw e; }
    } else {
      try { msg = (await res.text()).slice(0, 300) || msg; } catch { /* egal */ }
    }
    throw new ApiError(msg, res.status, detail, code);
  }
  if (res.status === 204) return null;
  return ct.includes('json') ? res.json() : res.text();
}

export async function get(path, opts = {}) {
  const res = await fetch(path, { headers: HEADERS, credentials: 'same-origin', ...opts });
  return handle(res);
}

export async function post(path, body, opts = {}) {
  const res = await fetch(path, {
    method: 'POST',
    headers: { ...HEADERS, 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: body === undefined ? undefined : JSON.stringify(body),
    ...opts,
  });
  return handle(res);
}

export async function del(path, body, opts = {}) {
  const res = await fetch(path, {
    method: 'DELETE',
    headers: { ...HEADERS, 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: body === undefined ? undefined : JSON.stringify(body),
    ...opts,
  });
  return handle(res);
}

/** Rohdaten senden (Upload eines Teils oder einer ganzen Datei). */
export function sendRaw(path, blob, { onProgress, signal, method = 'POST' } = {}) {
  // XMLHttpRequest statt fetch: nur damit gibt es verlässlichen
  // Fortschritt beim Hochladen in allen Browsern.
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open(method, path, true);
    xhr.withCredentials = true;
    xhr.setRequestHeader('X-SpeedNAS', '1');
    xhr.responseType = 'json';

    if (onProgress) {
      xhr.upload.onprogress = (e) => { if (e.lengthComputable) onProgress(e.loaded, e.total); };
    }
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) { resolve(xhr.response); return; }
      const r = xhr.response || {};
      const err = new ApiError(r.error || `Fehler ${xhr.status}`, xhr.status, r.detail, r.code);
      if (r.missing) err.missing = r.missing;
      reject(err);
    };
    xhr.onerror = () => reject(new ApiError('Netzwerkfehler beim Hochladen.', 0));
    xhr.ontimeout = () => reject(new ApiError('Zeitüberschreitung beim Hochladen.', 0));
    xhr.onabort = () => reject(new DOMException('abgebrochen', 'AbortError'));

    if (signal) {
      if (signal.aborted) { xhr.abort(); return; }
      signal.addEventListener('abort', () => xhr.abort(), { once: true });
    }
    xhr.send(blob);
  });
}

/**
 * Server-Sent-Events abonnieren. Liefert eine Funktion zum Beenden.
 * EventSource kann keine eigenen Header setzen - dafür läuft die
 * Authentifizierung über das Sitzungscookie.
 */
export function stream(path, handlers) {
  const es = new EventSource(path, { withCredentials: true });
  for (const [name, fn] of Object.entries(handlers)) {
    if (name === 'error') { es.onerror = fn; continue; }
    es.addEventListener(name, (ev) => {
      let data = null;
      try { data = JSON.parse(ev.data); } catch { /* nicht jedes Ereignis trägt JSON */ }
      fn(data, ev);
    });
  }
  return () => es.close();
}
