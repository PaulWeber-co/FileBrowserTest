// Verwaltung: Speicherorte, Diagnose, Leistung, Benutzer.

import { el, clear, icon, bytes, duration, url } from './util.js';
import * as api from './api.js';
import { dialog, confirm, prompt, toast, toastOk, showError, field, input, select, checkbox, kv, busy, emptyState } from './ui.js';

const TYPES = [
  { value: 'smb', label: 'SMB / Windows-Freigabe (Speedport, Fritzbox, NAS)' },
  { value: 'ftp', label: 'FTP / FTPS' },
  { value: 'sftp', label: 'SFTP (SSH)' },
  { value: 'webdav', label: 'WebDAV (Nextcloud, ownCloud)' },
  { value: 'local', label: 'Ordner auf diesem Rechner' },
];

/** Oeffnet den Einstellungsdialog. */
export function openSettings(app, tab = 'locations') {
  return dialog({
    title: 'Einstellungen',
    wide: true,
    build: ({ body, foot, close, box }) => {
      const tabs = el('div', { class: 'tabs' });
      const pane = el('div', { style: 'padding-top:14px' });
      body.before(tabs);
      body.append(pane);

      const items = [
        ['locations', 'Speicherorte', () => renderLocations(app, pane)],
        ['diagnose', 'Diagnose', () => renderDiagnose(app, pane)],
        ['performance', 'Leistung', () => renderPerf(app, pane)],
        ['shares', 'Freigabelinks', () => renderShares(app, pane)],
      ];
      if (app.state.me?.user?.admin) items.push(['users', 'Benutzer', () => renderUsers(app, pane)]);
      items.push(['about', 'Über', () => renderAbout(app, pane)]);

      const buttons = new Map();
      for (const [id, label, render] of items) {
        const b = el('button', { class: 'tab', onclick: () => activate(id) }, label);
        buttons.set(id, { b, render });
        tabs.append(b);
      }
      function activate(id) {
        for (const [k, v] of buttons) v.b.classList.toggle('active', k === id);
        clear(pane);
        buttons.get(id).render();
      }
      activate(buttons.has(tab) ? tab : 'locations');

      foot.append(el('button', { class: 'btn', onclick: () => close() }, 'Schließen'));
    },
  });
}

// ------------------------------------------------------- Speicherorte ----

async function renderLocations(app, pane) {
  pane.append(busy('Speicherorte werden geladen …'));
  let data;
  try {
    data = await api.get('/api/admin/locations');
  } catch (err) {
    clear(pane);
    pane.append(el('div', { class: 'notice error', text: err.message }));
    return;
  }
  clear(pane);

  const list = el('div');
  pane.append(list);
  pane.append(el('div', { style: 'margin:14px 0 6px;display:flex;gap:8px;flex-wrap:wrap' },
    el('button', { class: 'btn btn-primary', onclick: () => editLocation(app, null).then(() => renderLocations(app, clear(pane))) },
      icon('plus', 17), 'Speicherort hinzufügen'),
  ));

  if (!data.locations.length) {
    list.append(emptyState('server', 'Noch kein Speicherort',
      'Lege den ersten Speicherort an - für den Speedport ist das in der Regel eine SMB-Freigabe.'));
    return;
  }

  for (const loc of data.locations) {
    const idle = data.active?.[loc.id];
    list.append(el('div', {
      style: 'display:flex;align-items:center;gap:12px;padding:11px 12px;border:1px solid var(--border);'
        + 'border-radius:var(--radius-sm);margin-bottom:8px;background:var(--bg-elevated)',
    },
      el('div', { class: `fico ${loc.type === 'local' ? 'folder' : 'other'}` }, icon(loc.type === 'local' ? 'folder' : 'server')),
      el('div', { style: 'flex:1;min-width:0' },
        el('div', { style: 'font-weight:600;display:flex;align-items:center;gap:8px' },
          loc.label,
          loc.readOnly ? el('span', { class: 'tag', text: 'nur lesen' }) : null,
          idle !== undefined ? el('span', { class: 'tag ok', text: `${idle} Verb.` }) : null,
        ),
        el('div', { style: 'font-size:12px;color:var(--fg-subtle);overflow:hidden;text-overflow:ellipsis;white-space:nowrap' },
          describeLocation(loc)),
      ),
      el('button', { class: 'btn btn-sm', onclick: () => editLocation(app, loc).then(() => renderLocations(app, clear(pane))) }, 'Bearbeiten'),
      el('button', {
        class: 'btn btn-sm btn-danger',
        onclick: async () => {
          const ok = await confirm({
            title: 'Speicherort entfernen',
            message: `"${loc.label}" wirklich aus SpeedNAS entfernen?`,
            detail: 'Auf dem Netzwerkspeicher selbst wird nichts gelöscht - nur der Zugang hier verschwindet. Freigabelinks zu diesem Ort werden ungültig.',
            confirmLabel: 'Entfernen', danger: true,
          });
          if (!ok) return;
          try {
            await api.del(url('/api/admin/locations', { id: loc.id }));
            await app.reloadLocations();
            renderLocations(app, clear(pane));
          } catch (err) { showError(err, 'Entfernen fehlgeschlagen'); }
        },
      }, icon('trash', 15)),
    ));
  }
}

function describeLocation(loc) {
  switch (loc.type) {
    case 'smb': return `smb://${loc.smb?.host ?? '?'}/${loc.smb?.share ?? '?'}${loc.root ? '/' + loc.root : ''}`;
    case 'ftp': return `ftp://${loc.ftp?.host ?? '?'}${loc.ftp?.tls && loc.ftp.tls !== 'none' ? ' (TLS)' : ''}`;
    case 'sftp': return `sftp://${loc.sftp?.user ?? ''}@${loc.sftp?.host ?? '?'}`;
    case 'webdav': return loc.webdav?.url ?? '?';
    case 'local': return loc.local?.path ?? '?';
    default: return loc.type;
  }
}

/** Formular zum Anlegen/Bearbeiten eines Speicherorts. */
function editLocation(app, existing) {
  const loc = existing ? structuredClone(existing) : {
    type: 'smb', label: '', root: '', readOnly: false,
    smb: { host: '', share: '', user: '', password: '', dialect: '' },
  };

  return dialog({
    title: existing ? 'Speicherort bearbeiten' : 'Speicherort hinzufügen',
    wide: true,
    build: ({ body, foot, close }) => {
      const typeSel = select(TYPES, { value: loc.type });
      const labelIn = input({ value: loc.label, placeholder: 'z. B. Speedport USB' });
      const rootIn = input({ value: loc.root || '', placeholder: 'leer = ganze Freigabe' });
      const roBox = checkbox('Nur lesen (schützt vor versehentlichem Löschen)', loc.readOnly);
      const specific = el('div');
      const status = el('div');

      body.append(
        field('Typ', typeSel),
        field('Name', labelIn, 'So erscheint der Ort in der Seitenleiste.'),
        specific,
        field('Unterordner (optional)', rootIn, 'Beschränkt die Ansicht auf einen Unterordner der Freigabe.'),
        el('div', { class: 'field' }, roBox),
        status,
      );

      typeSel.addEventListener('change', () => {
        loc.type = typeSel.value;
        ensureBlock(loc);
        renderSpecific();
      });
      renderSpecific();

      function renderSpecific() {
        clear(specific);
        ensureBlock(loc);
        switch (loc.type) {
          case 'smb': smbForm(specific, loc, status); break;
          case 'ftp': ftpForm(specific, loc); break;
          case 'sftp': sftpForm(specific, loc); break;
          case 'webdav': webdavForm(specific, loc); break;
          case 'local': localForm(specific, loc); break;
        }
      }

      const collect = () => {
        loc.label = labelIn.value.trim() || defaultLabel(loc);
        loc.root = rootIn.value.trim();
        loc.readOnly = roBox.input.checked;
        return loc;
      };

      const testBtn = el('button', { class: 'btn' }, icon('wifi', 16), 'Verbindung testen');
      testBtn.addEventListener('click', async () => {
        testBtn.disabled = true;
        clear(status);
        status.append(busy('Verbindung wird getestet …'));
        try {
          const res = await api.post('/api/admin/test', collect());
          clear(status);
          if (res.ok) {
            status.append(el('div', { class: 'notice ok' },
              el('strong', {}, 'Verbindung steht. '),
              `${res.entries} Einträge in ${res.tookMs} ms.`,
              res.spaceText ? el('div', { style: 'margin-top:4px' }, res.spaceText) : null,
              res.sample?.length ? el('div', { style: 'margin-top:6px;font-size:12px;color:var(--fg-muted)' },
                'Gefunden: ' + res.sample.join(', ')) : null,
            ));
          } else {
            status.append(el('div', { class: 'notice error' },
              el('strong', {}, 'Fehlgeschlagen: '), res.error,
              loc.type === 'smb' ? el('div', { style: 'margin-top:8px' },
                el('button', { class: 'btn btn-sm', onclick: () => runProbe(loc.smb.host, loc.smb.port, status) },
                  icon('shield', 15), 'Protokoll prüfen')) : null,
            ));
          }
        } catch (err) {
          clear(status);
          status.append(el('div', { class: 'notice error', text: err.message }));
        }
        testBtn.disabled = false;
      });

      foot.append(
        el('div', { class: 'left' }, testBtn),
        el('button', { class: 'btn', onclick: () => close(false) }, 'Abbrechen'),
        el('button', {
          class: 'btn btn-primary',
          onclick: async (e) => {
            const btn = e.currentTarget;
            btn.disabled = true;
            try {
              await api.post('/api/admin/locations', collect());
              await app.reloadLocations();
              toastOk('Speicherort gespeichert.');
              close(true);
            } catch (err) {
              showError(err, 'Speichern fehlgeschlagen');
              btn.disabled = false;
            }
          },
        }, 'Speichern'),
      );
    },
  });
}

function ensureBlock(loc) {
  const defaults = {
    smb: { host: '', share: '', user: '', password: '', dialect: '' },
    ftp: { host: '', user: '', password: '', tls: 'none' },
    sftp: { host: '', user: '', password: '' },
    webdav: { url: '', user: '', password: '' },
    local: { path: '' },
  };
  if (!loc[loc.type]) loc[loc.type] = defaults[loc.type];
}

function defaultLabel(loc) {
  switch (loc.type) {
    case 'smb': return loc.smb.share || loc.smb.host || 'SMB';
    case 'ftp': return loc.ftp.host || 'FTP';
    case 'sftp': return loc.sftp.host || 'SFTP';
    case 'webdav': return 'WebDAV';
    case 'local': return loc.local.path?.split(/[\\/]/).pop() || 'Lokal';
    default: return 'Speicherort';
  }
}

function bind(inputEl, obj, key) {
  inputEl.addEventListener('input', () => { obj[key] = inputEl.value; });
  return inputEl;
}

function smbForm(host, loc, status) {
  const c = loc.smb;
  const hostIn = bind(input({ value: c.host, placeholder: '192.168.2.1' }), c, 'host');
  const shareIn = bind(input({ value: c.share, placeholder: 'z. B. USB-Speicher' }), c, 'share');
  const userIn = bind(input({ value: c.user || '', placeholder: 'leer = Gastzugang' }), c, 'user');
  const passIn = bind(input({ value: c.password || '', type: 'password', placeholder: 'leer lassen = unverändert' }), c, 'password');
  const dialectSel = select([
    { value: '', label: 'Automatisch (empfohlen)' },
    { value: '2.0.2', label: 'SMB 2.0.2 erzwingen (ältere Router)' },
    { value: '2.1', label: 'SMB 2.1 erzwingen' },
    { value: '3.0', label: 'SMB 3.0 erzwingen' },
    { value: '3.1.1', label: 'SMB 3.1.1 erzwingen' },
    { value: '1', label: 'SMB 1 (nur wenn der Router nichts anderes kann)' },
  ], { value: c.dialect || '' });

  // Warnung nur zeigen, wenn SMB1 tatsächlich gewählt ist.
  const smb1Warnung = el('div', { class: 'notice warn', hidden: true },
    el('strong', {}, 'SMB1 ist unverschlüsselt. '),
    'Wer im selben Netz mitliest, sieht deine Dateien und Metadaten im Klartext, '
    + 'und die Anmeldung ist schwächer als bei SMB2. '
    + 'Vertretbar ist das hier, weil SMB1 nur zwischen SpeedNAS und dem Router läuft: '
    + 'auch beim Zugriff von unterwegs verlässt es dein Netz nie. '
    + 'Im eigenen, WPA2-gesicherten WLAN ist das in Ordnung.',
    el('div', { style: 'margin-top:6px' },
      'Nimm SMB1 nur, wenn die Diagnose zeigt, dass der Router nichts Besseres anbietet. '
      + 'Die Hintergründe stehen in docs/smb1.md.'),
  );
  const zeigeWarnung = () => { smb1Warnung.hidden = dialectSel.value !== '1'; };
  dialectSel.addEventListener('change', () => { c.dialect = dialectSel.value; zeigeWarnung(); });
  zeigeWarnung();

  const discover = el('button', { class: 'btn btn-sm' }, icon('search', 15), 'Freigaben suchen');
  const shareRow = el('div', { style: 'display:flex;gap:8px;align-items:flex-start' },
    el('div', { style: 'flex:1' }, shareIn), discover);

  discover.addEventListener('click', async () => {
    if (!c.host) { toast('Bitte zuerst die Adresse eintragen.', { type: 'warn' }); return; }
    discover.disabled = true;
    clear(status);
    status.append(busy('Freigaben werden gesucht …'));
    try {
      const res = await api.post('/api/admin/shares/discover', {
        host: c.host, port: c.port || 0, user: c.user || '', password: c.password || '',
      });
      clear(status);
      if (!res.ok) {
        status.append(el('div', { class: 'notice warn' },
          res.error,
          el('div', { style: 'margin-top:8px' },
            el('button', { class: 'btn btn-sm', onclick: () => runProbe(c.host, c.port, status) },
              icon('shield', 15), 'Protokoll prüfen')),
        ));
      } else if (!res.shares.length) {
        status.append(el('div', { class: 'notice warn', text: 'Verbunden, aber keine Freigaben gefunden. Ist im Router der Netzwerkspeicher aktiviert?' }));
      } else {
        const box = el('div', { class: 'notice info' }, el('strong', {}, 'Gefundene Freigaben: '));
        for (const name of res.shares) {
          box.append(' ', el('button', {
            class: 'btn btn-sm',
            onclick: () => { shareIn.value = name; c.share = name; toastOk(`Freigabe "${name}" übernommen.`); },
          }, name));
        }
        status.append(box);
      }
    } catch (err) {
      clear(status);
      status.append(el('div', { class: 'notice error', text: err.message }));
    }
    discover.disabled = false;
  });

  host.append(
    field('Adresse des Routers / NAS', hostIn, 'Beim Speedport ist das meist <code>192.168.2.1</code>.'),
    field('Freigabename', shareRow, 'Der Name, unter dem der Router den USB-Speicher anbietet.'),
    el('div', { class: 'row' },
      field('Benutzer', userIn),
      field('Passwort', passIn),
    ),
    field('Protokollversion', dialectSel,
      'Nur ändern, wenn die automatische Aushandlung Probleme macht.'),
    smb1Warnung,
  );
}

function ftpForm(host, loc) {
  const c = loc.ftp;
  host.append(
    field('Adresse', bind(input({ value: c.host, placeholder: '192.168.2.1' }), c, 'host')),
    el('div', { class: 'row' },
      field('Benutzer', bind(input({ value: c.user || '', placeholder: 'leer = anonym' }), c, 'user')),
      field('Passwort', bind(input({ value: c.password || '', type: 'password' }), c, 'password')),
    ),
    field('Verschlüsselung', (() => {
      const s = select([
        { value: 'none', label: 'Keine (nur im Heimnetz vertretbar)' },
        { value: 'explicit', label: 'FTPS explizit (AUTH TLS)' },
        { value: 'implicit', label: 'FTPS implizit (Port 990)' },
      ], { value: c.tls || 'none' });
      s.addEventListener('change', () => { c.tls = s.value; });
      return s;
    })()),
    el('div', { class: 'field' }, checkbox('Passiv-Modus ohne EPSV (bei älteren Routern)', c.disableEpsv, (v) => { c.disableEpsv = v; })),
  );
}

function sftpForm(host, loc) {
  const c = loc.sftp;
  host.append(
    el('div', { class: 'row' },
      field('Adresse', bind(input({ value: c.host, placeholder: '192.168.2.10' }), c, 'host')),
      field('Port', bind(input({ value: c.port || '', placeholder: '22', type: 'number' }), c, 'port')),
    ),
    el('div', { class: 'row' },
      field('Benutzer', bind(input({ value: c.user || '' }), c, 'user')),
      field('Passwort', bind(input({ value: c.password || '', type: 'password' }), c, 'password')),
    ),
    field('Schlüsseldatei (optional)', bind(input({ value: c.keyFile || '', placeholder: 'C:\\Users\\...\\id_ed25519' }), c, 'keyFile'),
      'Wenn gesetzt, wird der Schlüssel statt des Passworts verwendet.'),
  );
}

function webdavForm(host, loc) {
  const c = loc.webdav;
  host.append(
    field('URL', bind(input({ value: c.url, placeholder: 'https://cloud.example.de/remote.php/dav/files/paul' }), c, 'url')),
    el('div', { class: 'row' },
      field('Benutzer', bind(input({ value: c.user || '' }), c, 'user')),
      field('Passwort', bind(input({ value: c.password || '', type: 'password' }), c, 'password')),
    ),
  );
}

function localForm(host, loc) {
  const c = loc.local;
  host.append(
    field('Pfad', bind(input({ value: c.path, placeholder: 'C:\\Users\\Paul\\Downloads' }), c, 'path'),
      'Ein Ordner auf dem Rechner, auf dem SpeedNAS läuft. Praktisch, um Dateien zwischen PC und NAS zu schieben.'),
  );
}

// ----------------------------------------------------------- Diagnose ----

async function renderDiagnose(app, pane) {
  const hostIn = input({ placeholder: '192.168.2.1', value: guessHost(app) });
  const out = el('div', { style: 'margin-top:12px' });

  pane.append(
    el('p', { style: 'margin-top:0;color:var(--fg-muted);line-height:1.6' },
      'Hier fragt SpeedNAS den Router direkt, welche SMB-Version er spricht. '
      + 'Das erklärt in den meisten Fällen, warum die iOS-Dateien-App oder der Windows-Explorer nicht verbinden.'),
    el('div', { style: 'display:flex;gap:8px;align-items:flex-end' },
      el('div', { style: 'flex:1' }, field('Adresse', hostIn)),
      el('button', {
        class: 'btn btn-primary', style: 'margin-bottom:14px',
        onclick: () => runProbe(hostIn.value.trim(), 0, out),
      }, icon('shield', 16), 'Prüfen'),
    ),
    out,
  );

  pane.append(el('hr', { style: 'border:0;border-top:1px solid var(--border);margin:22px 0 16px' }));
  pane.append(el('h3', { style: 'margin:0 0 4px;font-size:14px' }, 'Geschwindigkeitstest'));
  pane.append(el('p', { style: 'margin:0 0 12px;color:var(--fg-muted);font-size:13px;line-height:1.6' },
    'Misst den echten Durchsatz - einmal mit einer einzelnen Leseanfrage, einmal mit parallelen. '
    + 'Der Unterschied zeigt, wie viel die Latenz kostet.'));

  const locSel = select(app.state.locations.map((l) => ({ value: l.id, label: l.label })),
    { value: app.state.loc?.id });
  const mbSel = select([
    { value: '16', label: '16 MB (schnell)' },
    { value: '32', label: '32 MB' },
    { value: '64', label: '64 MB (genauer)' },
  ], { value: '32' });
  const writeBox = checkbox('Auch Schreibgeschwindigkeit messen (legt kurz eine Testdatei an)', false);
  const speedOut = el('div', { style: 'margin-top:12px' });

  pane.append(
    el('div', { class: 'row' }, field('Speicherort', locSel), field('Datenmenge', mbSel)),
    el('div', { class: 'field' }, writeBox),
    el('button', {
      class: 'btn btn-primary',
      onclick: async (e) => {
        const btn = e.currentTarget;
        btn.disabled = true;
        clear(speedOut);
        speedOut.append(busy('Messung läuft - das kann eine Minute dauern …'));
        try {
          const res = await api.post('/api/admin/speedtest', {
            loc: locSel.value, mb: Number(mbSel.value), write: writeBox.input.checked,
          });
          clear(speedOut);
          speedOut.append(renderSpeedResult(res));
        } catch (err) {
          clear(speedOut);
          speedOut.append(el('div', { class: 'notice error', text: err.message }));
        }
        btn.disabled = false;
      },
    }, icon('gauge', 16), 'Messen'),
    speedOut,
  );
}

function guessHost(app) {
  const smb = app.state.rawLocations?.find((l) => l.type === 'smb');
  return smb?.smb?.host || '';
}

async function runProbe(host, port, out) {
  if (!host) { toast('Bitte eine Adresse eingeben.', { type: 'warn' }); return; }
  clear(out);
  out.append(busy(`${host} wird geprüft …`));
  try {
    const { probe: p, hints } = await api.post('/api/admin/probe', { host, port: port || 0 });
    clear(out);
    const tag = (ok, yes, no) => el('span', { class: `tag ${ok ? 'ok' : 'bad'}`, text: ok ? yes : no });
    out.append(
      el('div', { class: 'notice info' },
        kv([
          ['Erreichbar', tag(p.reachable, 'ja', 'nein')],
          ['Antwortzeit', p.reachable ? `${p.rttMs.toFixed(1)} ms` : null],
          ['SMB2/3', p.smb2 ? el('span', { class: 'tag ok', text: p.dialectName }) : tag(false, '', 'nein')],
          ['SMB1', p.smb1 ? el('span', { class: 'tag warn', text: p.smb1Dialect || 'ja' }) : el('span', { class: 'tag', text: 'nein' })],
          ['Signierung', p.smb2 ? (p.signingRequired ? el('span', { class: 'tag warn', text: 'erzwungen' }) : 'optional') : null],
          ['Max. Leseblock', p.maxReadSize ? `${Math.round(p.maxReadSize / 1024)} KiB` : null],
          ['Max. Schreibblock', p.maxWriteSize ? `${Math.round(p.maxWriteSize / 1024)} KiB` : null],
        ]),
      ),
      hints?.length ? el('div', { class: 'notice warn' },
        el('strong', {}, 'Einschätzung'),
        el('ul', {}, ...hints.map((h) => el('li', { text: h }))),
      ) : null,
    );
  } catch (err) {
    clear(out);
    out.append(el('div', { class: 'notice error', text: err.message }));
  }
}

function renderSpeedResult(res) {
  const box = el('div');
  const rows = [];
  if (res.file) rows.push(['Testdatei', res.file]);
  if (res.bytes) rows.push(['Gelesen', bytes(res.bytes)]);
  if (res.serialMBs) rows.push(['Eine Leseanfrage', `${res.serialMBs.toFixed(1)} MB/s`]);
  if (res.parallelMBs) rows.push([`${res.workers} parallele Anfragen`, `${res.parallelMBs.toFixed(1)} MB/s`]);
  if (res.writeMBs) rows.push(['Schreiben', `${res.writeMBs.toFixed(1)} MB/s`]);
  if (res.pingMs !== undefined) rows.push(['Antwortzeit', `${res.pingMs.toFixed(1)} ms`]);
  box.append(el('div', { class: 'notice info' }, kv(rows)));

  if (res.readError) box.append(el('div', { class: 'notice error', text: res.readError }));
  if (res.writeError) box.append(el('div', { class: 'notice error', text: `Schreiben: ${res.writeError}` }));
  if (res.hints?.length) {
    box.append(el('div', { class: 'notice warn' },
      el('strong', {}, 'Hinweise'),
      el('ul', {}, ...res.hints.map((h) => el('li', { text: h }))),
    ));
  }
  return box;
}

// ------------------------------------------------------------ Leistung ---

async function renderPerf(app, pane) {
  pane.append(busy());
  let perf, status;
  try {
    [perf, status] = await Promise.all([api.get('/api/admin/perf'), api.get('/api/admin/status')]);
  } catch (err) {
    clear(pane);
    pane.append(el('div', { class: 'notice error', text: err.message }));
    return;
  }
  clear(pane);

  const workers = input({ type: 'number', min: 1, max: 16, value: perf.prefetchWorkers });
  const chunk = input({ type: 'number', min: 64, max: 8192, step: 64, value: perf.prefetchChunkKb });
  const cache = input({ type: 'number', min: 0, max: 300, value: perf.listCacheSeconds });
  const thumbMB = input({ type: 'number', min: 32, max: 20000, value: perf.thumbCacheMb });
  const thumbW = input({ type: 'number', min: 1, max: 8, value: perf.thumbWorkers });
  const searchW = input({ type: 'number', min: 1, max: 16, value: perf.searchWorkers });
  const partMB = input({ type: 'number', min: 1, max: 64, value: perf.uploadPartMb });

  pane.append(
    el('div', { class: 'notice info' },
      el('strong', {}, 'Der wichtigste Regler ist der erste. '),
      'Über VPN oder WLAN bremst nicht die Bandbreite, sondern die Wartezeit pro Anfrage. '
      + 'Mehrere Anfragen gleichzeitig halten die Leitung gefüllt. Im gleichen Netz reichen 4, '
      + 'über VPN sind 6 bis 8 oft deutlich schneller.'),
    el('div', { class: 'row' },
      field('Parallele Leseanfragen', workers, '1 = klassisch seriell. Mehr hilft bei hoher Antwortzeit.'),
      field('Blockgröße (KB)', chunk, 'Nicht größer als der maximale Leseblock des Servers (siehe Diagnose).'),
    ),
    el('div', { class: 'row' },
      field('Verzeichnis-Cache (Sekunden)', cache, '0 schaltet den Cache ab. 5 ist ein guter Wert.'),
      field('Upload-Teilgröße (MB)', partMB, 'Kleinere Teile werden nach einem Abbruch schneller nachgeholt.'),
    ),
    el('div', { class: 'row' },
      field('Vorschaubild-Cache (MB)', thumbMB),
      field('Vorschau-Prozesse', thumbW),
    ),
    field('Parallele Ordner bei der Suche', searchW),
    el('div', { style: 'display:flex;gap:8px;flex-wrap:wrap;margin:6px 0 18px' },
      el('button', {
        class: 'btn btn-primary',
        onclick: async (e) => {
          e.currentTarget.disabled = true;
          try {
            await api.post('/api/admin/perf', {
              prefetchWorkers: +workers.value, prefetchChunkKb: +chunk.value,
              listCacheSeconds: +cache.value, thumbCacheMb: +thumbMB.value,
              thumbWorkers: +thumbW.value, searchWorkers: +searchW.value,
              uploadPartMb: +partMB.value,
            });
            toastOk('Gespeichert. Verbindungen wurden neu aufgebaut.');
          } catch (err) { showError(err, 'Speichern fehlgeschlagen'); }
          e.currentTarget.disabled = false;
        },
      }, 'Speichern'),
      el('button', {
        class: 'btn',
        onclick: async () => {
          const ok = await confirm({
            title: 'Cache leeren',
            message: 'Vorschaubilder und Verzeichnis-Cache verwerfen?',
            confirmLabel: 'Leeren',
          });
          if (!ok) return;
          try { await api.post('/api/admin/cache/clear'); toastOk('Cache geleert.'); } catch (err) { showError(err); }
        },
      }, icon('trash', 16), 'Cache leeren'),
    ),
    el('h3', { style: 'font-size:14px;margin:0 0 8px' }, 'Status'),
    el('div', { class: 'notice info' }, kv([
      ['Laufzeit', duration(status.uptimeSec)],
      ['Anfragen', String(status.requests)],
      ['Ausgeliefert', bytes(status.bytesOut)],
      ['Empfangen', bytes(status.bytesIn)],
      ['Vorschaucache', `${bytes(status.thumbBytes)} in ${status.thumbFiles} Dateien`],
      ['Speicher', `${status.memMB} MB`],
      ['Goroutinen', String(status.goroutines)],
      ['ffmpeg', status.ffmpeg
        ? el('span', { class: 'tag ok', text: 'gefunden' })
        : el('span', { class: 'tag', text: 'nicht gefunden - keine Videovorschau' })],
      ['System', `${status.os}, ${status.go}`],
      ['Konfiguration', status.configPath],
    ])),
  );
}

// -------------------------------------------------------- Freigabelinks --

async function renderShares(app, pane) {
  pane.append(busy());
  let data;
  try { data = await api.get('/api/shares'); } catch (err) {
    clear(pane); pane.append(el('div', { class: 'notice error', text: err.message })); return;
  }
  clear(pane);
  if (!data.shares.length) {
    pane.append(emptyState('link', 'Keine Freigabelinks',
      'Über das Kontextmenü einer Datei lässt sich ein Link erzeugen, den auch Leute ohne Zugang öffnen können.'));
    return;
  }
  for (const { share: s, url: link } of data.shares) {
    pane.append(el('div', {
      style: 'display:flex;align-items:center;gap:12px;padding:11px 12px;border:1px solid var(--border);'
        + 'border-radius:var(--radius-sm);margin-bottom:8px',
    },
      el('div', { class: 'fico other' }, icon(s.isDir ? 'folder' : 'link')),
      el('div', { style: 'flex:1;min-width:0' },
        el('div', { style: 'font-weight:600' }, s.name),
        el('div', { style: 'font-size:12px;color:var(--fg-subtle);overflow:hidden;text-overflow:ellipsis' }, link),
        el('div', { style: 'font-size:12px;color:var(--fg-subtle);margin-top:2px' },
          `${s.hits} Aufrufe`,
          s.hasPassword ? ' · passwortgeschützt' : '',
          s.expires ? ` · läuft ab ${new Date(s.expires).toLocaleDateString('de-DE')}` : ' · unbegrenzt'),
      ),
      el('button', {
        class: 'btn btn-sm',
        onclick: async () => {
          const { copyText } = await import('./util.js');
          (await copyText(link)) ? toastOk('Link kopiert.') : toast('Kopieren nicht möglich.', { type: 'warn' });
        },
      }, icon('copy', 15)),
      el('button', {
        class: 'btn btn-sm btn-danger',
        onclick: async () => {
          try { await api.del('/api/shares', { token: s.token }); renderShares(app, clear(pane)); } catch (err) { showError(err); }
        },
      }, icon('trash', 15)),
    ));
  }
}

// ------------------------------------------------------------ Benutzer ---

async function renderUsers(app, pane) {
  pane.append(busy());
  let data;
  try { data = await api.get('/api/admin/users'); } catch (err) {
    clear(pane); pane.append(el('div', { class: 'notice error', text: err.message })); return;
  }
  clear(pane);

  for (const u of data.users) {
    pane.append(el('div', {
      style: 'display:flex;align-items:center;gap:12px;padding:10px 12px;border:1px solid var(--border);'
        + 'border-radius:var(--radius-sm);margin-bottom:8px',
    },
      el('div', { class: 'fico other' }, icon('users')),
      el('div', { style: 'flex:1' },
        el('div', { style: 'font-weight:600' }, u.name),
        el('div', { style: 'font-size:12px;color:var(--fg-subtle)' },
          [u.admin ? 'Administrator' : 'Benutzer', u.readOnly ? 'nur lesen' : null].filter(Boolean).join(' · ')),
      ),
      el('button', { class: 'btn btn-sm', onclick: () => editUser(app, u).then(() => renderUsers(app, clear(pane))) }, 'Bearbeiten'),
      el('button', {
        class: 'btn btn-sm btn-danger',
        onclick: async () => {
          const ok = await confirm({ title: 'Benutzer löschen', message: `"${u.name}" wirklich löschen?`, danger: true, confirmLabel: 'Löschen' });
          if (!ok) return;
          try { await api.del(url('/api/admin/users', { name: u.name })); renderUsers(app, clear(pane)); } catch (err) { showError(err); }
        },
      }, icon('trash', 15)),
    ));
  }
  pane.append(el('button', {
    class: 'btn btn-primary', style: 'margin-top:8px',
    onclick: () => editUser(app, null).then(() => renderUsers(app, clear(pane))),
  }, icon('plus', 16), 'Benutzer hinzufügen'));
}

function editUser(app, existing) {
  return dialog({
    title: existing ? `Benutzer ${existing.name}` : 'Neuer Benutzer',
    build: ({ body, foot, close }) => {
      const nameIn = input({ value: existing?.name || '', disabled: !!existing });
      const passIn = input({ type: 'password', placeholder: existing ? 'leer = unverändert' : 'mindestens 8 Zeichen' });
      const adminBox = checkbox('Administrator (darf Einstellungen ändern)', existing?.admin || false);
      const roBox = checkbox('Nur lesen', existing?.readOnly || false);
      body.append(field('Benutzername', nameIn), field('Passwort', passIn),
        el('div', { class: 'field' }, adminBox), el('div', { class: 'field' }, roBox));
      foot.append(
        el('button', { class: 'btn', onclick: () => close(false) }, 'Abbrechen'),
        el('button', {
          class: 'btn btn-primary',
          onclick: async () => {
            try {
              await api.post('/api/admin/users', {
                name: nameIn.value.trim(), password: passIn.value,
                admin: adminBox.input.checked, readOnly: roBox.input.checked,
              });
              toastOk('Gespeichert.');
              close(true);
            } catch (err) { showError(err, 'Speichern fehlgeschlagen'); }
          },
        }, 'Speichern'),
      );
    },
  });
}

// ---------------------------------------------------------------- Über --

function renderAbout(app, pane) {
  pane.append(
    el('div', { style: 'display:flex;gap:14px;align-items:center;margin-bottom:14px' },
      el('img', { src: '/static/icons/icon-192.png', width: 56, height: 56, style: 'border-radius:14px' }),
      el('div', {},
        el('div', { style: 'font-size:17px;font-weight:650' }, 'SpeedNAS'),
        el('div', { style: 'color:var(--fg-muted)' }, `Version ${app.state.me?.version || '?'}`),
      ),
    ),
    el('p', { style: 'line-height:1.65;color:var(--fg-muted)' },
      'Ein Dateibrowser für Netzwerkspeicher, der im eigenen Netz läuft. '
      + 'Er spricht SMB, FTP, SFTP und WebDAV und stellt alles als Weboberfläche bereit - '
      + 'damit kommt auch das iPhone an Freigaben, für die es sonst eine App bräuchte.'),
    el('div', { class: 'notice info' },
      el('strong', {}, 'Als App aufs Handy: '),
      'Seite in Safari oder Chrome öffnen, Teilen-Menü, "Zum Home-Bildschirm". '
      + 'Dann startet SpeedNAS im Vollbild wie eine normale App.'),
    el('h3', { style: 'font-size:14px;margin:18px 0 8px' }, 'Tastenkürzel'),
    el('div', { class: 'notice info' }, kv([
      ['Strg + A', 'alles auswählen'],
      ['Strg + C / X / V', 'kopieren, ausschneiden, einfügen'],
      ['Entf', 'löschen'],
      ['F2', 'umbenennen'],
      ['Strg + F', 'suchen'],
      ['Rückschritt', 'eine Ebene hoch'],
      ['Eingabe', 'öffnen'],
      ['Esc', 'Auswahl aufheben / schließen'],
      ['?', 'diese Liste'],
    ])),
  );
}
