'use strict';

// Simulated accounts: there is no login yet. The selected user id is stored in
// localStorage and sent to the API as X-User-ID on every request.
const USER_STORAGE_KEY = 'yoloUserId';

function currentUserID() {
  return localStorage.getItem(USER_STORAGE_KEY) || '';
}

function setCurrentUserID(id) {
  if (id) localStorage.setItem(USER_STORAGE_KEY, id);
  else localStorage.removeItem(USER_STORAGE_KEY);
}

function userHeader(headers) {
  const merged = new Headers(headers || {});
  const id = currentUserID();
  if (id) merged.set('X-User-ID', id);
  return merged;
}

function userFetch(path, options = {}) {
  return fetch(path, { ...options, headers: userHeader(options.headers) });
}

function userLabel(user) {
  if (!user) return '';
  return `${user.display_name} (@${user.username})`;
}

async function initUserSelect(select) {
  if (!select) return null;
  try {
    const res = await fetch('/api/users');
    if (!res.ok) throw new Error('โหลดรายชื่อ user ไม่สำเร็จ');
    const data = await res.json();
    const users = data.users || [];
    let current = users.find((u) => u.id === currentUserID());
    if (!current) {
      current = users[0] || null;
      setCurrentUserID(current ? current.id : '');
    }

    select.innerHTML = '';
    for (const user of users) {
      const option = document.createElement('option');
      option.value = user.id;
      option.textContent = userLabel(user);
      select.appendChild(option);
    }
    if (current) select.value = current.id;
    select.title = 'สลับผู้ใช้จำลอง';
    select.addEventListener('change', () => {
      setCurrentUserID(select.value);
      window.location.reload();
    });
    return current;
  } catch (err) {
    select.innerHTML = '';
    const option = document.createElement('option');
    option.textContent = 'โหลด user ไม่สำเร็จ';
    select.appendChild(option);
    select.disabled = true;
    console.warn(err);
    return null;
  }
}
