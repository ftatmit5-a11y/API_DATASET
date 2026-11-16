// Web asset consistency checks used by CI and by `node scripts/check-web.mjs`.
//
// 1. Every file listed in the //go:embed directive must exist in web/.
// 2. Every id looked up with getElementById in a page script must exist in the
//    matching HTML file.
// 3. Any page whose script calls the helpers from common.js must load it first.

import { existsSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const web = join(root, 'web');

const pages = [
  ['annotate.html', 'annotate.js'],
  ['projects.html', 'projects.js'],
  ['project.html', 'project.js'],
  ['trash.html', 'trash.js'],
  ['pending.html', 'pending.js'],
  ['mywork.html', 'mywork.js'],
  ['users.html', 'users.js'],
  ['login.html', 'login.js'],
  ['index.html', 'app.js'],
];

// Names that must not be redeclared with const/let by a page script, because
// common.js already owns them at global scope.
const sharedHelpers = [
  'mkEl', 'request', 'userFetch', 'fetchMe', 'requireUser', 'renderIdentity',
  'goToLogin', 'homePath', 'isTeacher', 'isStudent', 'jsonOptions', 'toastBanner', 'logout',
];

const problems = [];

// ---------- 1. go:embed list ----------

const embedSource = readFileSync(join(web, 'embed.go'), 'utf8');
const embedLine = embedSource.split('\n').find((line) => line.includes('//go:embed'));
if (!embedLine) {
  problems.push('web/embed.go: no //go:embed directive found');
} else {
  const listed = embedLine
    .replace('//go:embed', '')
    .trim()
    .split(/\s+/)
    .filter((name) => name && !name.includes('*'));
  for (const name of listed) {
    if (!existsSync(join(web, name))) {
      problems.push(`web/embed.go: embedded file missing -> web/${name}`);
    }
  }
  for (const [html, js] of pages) {
    for (const name of [html, js]) {
      if (!listed.includes(name) && !name.includes('*')) {
        problems.push(`web/embed.go: ${name} is used by a page but not embedded`);
      }
    }
  }
}

// ---------- 2. getElementById ids and 3. common.js ----------

for (const [htmlName, jsName] of pages) {
  const html = readFileSync(join(web, htmlName), 'utf8');
  const js = readFileSync(join(web, jsName), 'utf8');

  const ids = [...js.matchAll(/getElementById\('([^']+)'\)/g)].map((match) => match[1]);
  const missing = [...new Set(ids)].filter((id) => !html.includes(`id="${id}"`));
  if (missing.length) {
    problems.push(`${jsName}: ids not found in ${htmlName} -> ${missing.join(', ')}`);
  }

  const usesCommon = /\b(userFetch|initUserSelect|currentUserID|userHeader)\s*\(/.test(js);
  const loadsCommon = /<script src="common\.js"><\/script>/.test(html);
  if (usesCommon && !loadsCommon) {
    problems.push(`${htmlName}: ${jsName} uses common.js helpers but the page does not load common.js`);
  }
  if (loadsCommon) {
    const commonIndex = html.indexOf('src="common.js"');
    const ownIndex = html.indexOf(`src="${jsName}"`);
    if (ownIndex !== -1 && commonIndex > ownIndex) {
      problems.push(`${htmlName}: common.js must be loaded before ${jsName}`);
    }
  }

  // A page that lexically redeclares a name common.js already owns (const/let)
  // throws "Identifier has already been declared" at load. Re-declaring with a
  // function declaration is legal, so only const/let are checked here.
  if (loadsCommon) {
    const shadow = js.match(
      new RegExp(`^(?:const|let)\\s+(${sharedHelpers.join('|')})\\b`, 'm'),
    );
    if (shadow) {
      problems.push(`${jsName}: const/let redeclares common.js helper "${shadow[1]}" and would throw at load`);
    }
  }
}

if (problems.length) {
  console.error('Web asset checks failed:\n  ' + problems.join('\n  '));
  process.exit(1);
}

console.log(`Web asset checks passed (${pages.length} pages).`);
