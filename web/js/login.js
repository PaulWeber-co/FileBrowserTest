// Anmeldung und Ersteinrichtung.

import * as api from './api.js';

const form = document.getElementById('form');
const errorBox = document.getElementById('error');
const submit = document.getElementById('submit');
const pass2Field = document.getElementById('pass2-field');
const pass2 = document.getElementById('pass2');

let setupMode = false;

applyTheme();

api.get('/api/me').then((me) => {
  if (me.authenticated) { location.href = '/'; return; }
  if (me.needSetup) {
    setupMode = true;
    document.getElementById('title').textContent = 'Willkommen';
    document.getElementById('lead').textContent =
      'Lege den ersten Zugang an. Damit meldest du dich künftig bei SpeedNAS an - '
      + 'die Zugangsdaten deines Netzwerkspeichers kommen erst danach.';
    document.getElementById('pass').autocomplete = 'new-password';
    pass2Field.hidden = false;
    pass2.required = true;
    submit.textContent = 'Zugang anlegen';
  }
}).catch(() => {});

form.addEventListener('submit', async (e) => {
  e.preventDefault();
  errorBox.hidden = true;
  const user = document.getElementById('user').value.trim();
  const password = document.getElementById('pass').value;

  if (setupMode) {
    if (password.length < 8) return fail('Bitte mindestens 8 Zeichen verwenden.');
    if (password !== pass2.value) return fail('Die Passwörter stimmen nicht überein.');
  }

  submit.disabled = true;
  submit.textContent = setupMode ? 'Wird angelegt …' : 'Wird geprüft …';
  try {
    await api.post(setupMode ? '/api/setup' : '/api/login', { user, password });
    location.href = '/';
  } catch (err) {
    fail(err.message || 'Anmeldung fehlgeschlagen.');
    submit.disabled = false;
    submit.textContent = setupMode ? 'Zugang anlegen' : 'Anmelden';
  }
});

function fail(msg) {
  errorBox.textContent = msg;
  errorBox.hidden = false;
}

function applyTheme() {
  const t = localStorage.getItem('theme');
  if (t && t !== 'auto') document.documentElement.setAttribute('data-theme', t);
}
