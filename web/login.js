'use strict';

const form = document.getElementById('login-form');
const usernameInput = document.getElementById('login-username');
const passwordInput = document.getElementById('login-password');
const errorBox = document.getElementById('login-error');
const submitBtn = document.getElementById('login-submit');

function showError(text) {
  errorBox.textContent = text;
  errorBox.hidden = false;
}

function clearError() {
  errorBox.textContent = '';
  errorBox.hidden = true;
}

// targetPath honours ?next= but only for same-origin paths, so the login page
// cannot be turned into an open redirect.
function targetPath(user) {
  const next = new URL(window.location.href).searchParams.get('next') || '';
  if (next.startsWith('/') && !next.startsWith('//')) return next;
  return homePath(user);
}

async function submit(event) {
  event.preventDefault();
  clearError();
  submitBtn.disabled = true;
  try {
    const data = await api('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        username: usernameInput.value.trim(),
        password: passwordInput.value,
      }),
    });
    window.location.replace(targetPath(data.user));
  } catch (err) {
    showError(err.message);
    passwordInput.select();
  } finally {
    submitBtn.disabled = false;
  }
}

// Someone already signed in should not see the form again.
(async function boot() {
  const res = await request('/api/auth/me');
  if (res.ok) {
    const data = await res.json();
    if (data && data.user) {
      window.location.replace(targetPath(data.user));
      return;
    }
  }
  usernameInput.focus();
})();

form.addEventListener('submit', submit);
