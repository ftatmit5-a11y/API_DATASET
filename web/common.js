'use strict';

// Real accounts now: the browser holds only a session cookie set by the server.
// Nothing about the signed-in user is trusted from localStorage.
const CSRF_HEADER = 'X-Requested-With';
const CSRF_TOKEN = 'yolo-web';

// request sends a fetch with the CSRF header that writes require. Every page
// script must use it instead of fetch() so a forged cross-site form cannot
// change data.
async function request(path, options = {}) {
  const headers = new Headers(options.headers || {});
  headers.set(CSRF_HEADER, CSRF_TOKEN);
  const res = await fetch(path, { ...options, headers, credentials: 'same-origin' });
  return res;
}

// api is request() plus JSON parsing and error handling.
async function api(path, options = {}) {
  const res = await request(path, options);
  const text = await res.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (res.status === 401) {
    goToLogin();
    throw new Error((data && data.error) || 'please sign in');
  }
  if (!res.ok) {
    throw new Error((data && data.error) || text || res.statusText);
  }
  return data;
}

// userFetch stays as the name used by older page scripts.
function userFetch(path, options = {}) {
  return request(path, options);
}

function jsonOptions(method, value) {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(value),
  };
}

function goToLogin() {
  const here = window.location.pathname + window.location.search;
  const target = new URL('/login.html', window.location.href);
  if (!here.startsWith('/login')) {
    target.searchParams.set('next', here);
  }
  window.location.replace(target.toString());
}

async function fetchMe() {
  const res = await request('/api/auth/me');
  if (!res.ok) {
    goToLogin();
    throw new Error('please sign in');
  }
  const data = await res.json();
  return data.user || null;
}

const ROLE_TEACHER = 'teacher';
const ROLE_STUDENT = 'student';

function isTeacher(user) {
  return Boolean(user && user.role === ROLE_TEACHER);
}

function isStudent(user) {
  return Boolean(user && user.role === ROLE_STUDENT);
}

// Landing pages differ by role: teachers manage projects, students submit work.
function homePath(user) {
  return isTeacher(user) ? '/projects.html' : '/mywork.html';
}

// requireUser loads the session and stops the page when the account is not the
// expected role. Returns the user so callers can render with it.
async function requireUser(role) {
  const user = await fetchMe();
  if (role === ROLE_TEACHER && !isTeacher(user)) {
    toastBanner('หน้านี้สำหรับอาจารย์เท่านั้น');
    window.location.replace('/mywork.html');
    return null;
  }
  if (role === ROLE_STUDENT && !isStudent(user)) {
    window.location.replace('/projects.html');
    return null;
  }
  return user;
}

// renderIdentity replaces the old user picker with the signed-in account,
// its role, a logout button, and a password change link.
function renderIdentity(user, elements) {
  if (!user || !elements || !elements.identity) return;
  elements.identity.textContent = `${user.display_name || user.username} · ${
    isTeacher(user) ? 'อาจารย์' : 'นักศึกษา'
  }`;
  elements.identity.title = `บัญชี ${user.username}`;
  if (elements.roleTag) {
    elements.roleTag.textContent = isTeacher(user) ? 'อาจารย์' : 'นักศึกษา';
    elements.roleTag.className = 'role-tag ' + (isTeacher(user) ? 'role-teacher' : 'role-student');
  }
  const logoutBtn = elements.logoutBtn || elements.logout;
  if (logoutBtn) {
    logoutBtn.addEventListener('click', logout);
  }
  renderNav(user);
}

// renderNav hides teacher-only navigation links when the signed-in user is a
// student. Links that should be teacher-only must have data-teacher-only.
function renderNav(user) {
  const teacher = isTeacher(user);
  document.querySelectorAll('[data-teacher-only]').forEach((link) => {
    link.hidden = !teacher;
  });
}

async function logout() {
  try {
    await api('/api/auth/logout', { method: 'POST' });
  } catch {
    // The cookie is gone either way; landing on the login page is the goal.
  }
  window.location.replace('/login.html');
}

// toastBanner is used before a page's own toast exists (role redirects).
function toastBanner(text) {
  const box = document.createElement('div');
  box.className = 'toast show error';
  box.textContent = text;
  box.style.position = 'fixed';
  box.style.top = '1rem';
  box.style.left = '50%';
  box.style.transform = 'translateX(-50%)';
  box.style.zIndex = '999';
  document.body.appendChild(box);
  setTimeout(() => box.remove(), 3000);
}

// mkEl builds one element. It is deliberately NOT named "el", because every
// page script keeps its element map in a top-level `const el`.
function mkEl(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = text;
  return node;
}

function fmtDate(value) {
  if (!value) return '';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleString('th-TH', { dateStyle: 'medium', timeStyle: 'short' });
}
