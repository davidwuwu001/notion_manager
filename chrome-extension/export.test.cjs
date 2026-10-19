const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function environment() {
  const elements = new Map();
  const clicked = [];
  const make = () => ({ value: '', disabled: false, classList: { add() {}, remove() {} }, addEventListener() {}, replaceChildren() {}, appendChild() {}, remove() {}, click() { clicked.push({ download: this.download, href: this.href }); } });
  const document = {
    getElementById(id) { if (!elements.has(id)) elements.set(id, make()); return elements.get(id); },
    createElement: make,
    body: { appendChild() {} },
    addEventListener() {},
  };
  let blob;
  const context = vm.createContext({ document, localStorage: { getItem: () => 'zh' }, Blob,
    URL: { createObjectURL(value) { blob = value; return 'blob:fixture'; }, revokeObjectURL() {} },
    setTimeout() {},
  });
  vm.runInContext(fs.readFileSync(require.resolve('./popup.js'), 'utf8'), context);
  vm.runInContext(`extractedConfig = {token_v2:'fixture-token',user_id:'current-user',user_name:'Same User',full_cookie:'not exported'};
    extractedWorkspaces = [{id:'space-a',name:'Same Name',plan_type:'Business Trial',business_verified:false},{id:'space-b',name:'Same Name',plan_type:'business',business_verified:true}];`, context);
  return { context, elements, document, clicked, blob: () => blob };
}

test('export requires explicit workspace selection and retains its exact ID', async () => {
  const env = environment();
  vm.runInContext('selectWorkspace(); exportConfig();', env.context);
  assert.equal(env.elements.get('exportBtn').disabled, true);
  assert.equal(env.clicked.length, 0);
  env.document.getElementById('workspaceSelect').value = 'space-b';
  vm.runInContext('selectWorkspace(); exportConfig();', env.context);
  assert.equal(env.elements.get('exportBtn').disabled, false);
  assert.equal(env.clicked[0].download, 'notion-account-space-b.json');
  const config = JSON.parse(await env.blob().text());
  assert.equal(config.space_id, 'space-b');
  assert.equal(config.user_id, 'current-user');
  assert.equal(config.token_v2, 'fixture-token');
  assert.equal(config.full_cookie, undefined);
});

test('trial and unverified workspaces cannot be exported even if selected', () => {
  const env = environment();
  env.document.getElementById('workspaceSelect').value = 'space-a';
  vm.runInContext('selectWorkspace(); exportConfig();', env.context);
  assert.equal(env.elements.get('exportBtn').disabled, true);
  assert.equal(env.clicked.length, 0);
  assert.equal(env.elements.get('configJson').value, '');
});

test('copy works without opening sensitive JSON and reports clipboard errors', async () => {
  const env = environment();
  env.document.getElementById('workspaceSelect').value = 'space-b';
  let copied;
  env.context.navigator = { clipboard: { writeText: async text => { copied = text; } } };
  vm.runInContext('selectWorkspace()', env.context);
  await vm.runInContext('copyConfig()', env.context);
  assert.equal(JSON.parse(copied).space_id, 'space-b');
  assert.match(env.document.getElementById('copyBtn').textContent, /已复制/);
  env.context.navigator.clipboard.writeText = async () => { throw new Error('denied'); };
  await vm.runInContext('copyConfig()', env.context);
  assert.match(env.document.getElementById('copyBtn').textContent, /复制失败/);
});

test('reads the selected login identity and its billing even when Notion defaults to another user', async () => {
  const env = environment();
  Object.assign(env.context, {
    readCurrentNotionSession: async () => ({ tab: { id: 1, url: 'https://app.notion.com' }, token: 'fixture', userId: 'u' }),
    crypto: { randomUUID: () => 'fixture' },
    window: { CONFIG: { version: 'fixture' } },
    chrome: { cookies: { getAll: async () => [] }, scripting: { executeScript: async ({ func, args }) => [{ result: await func(...args) }] } },
    fetch: async (url, options) => ({ ok: true, json: async () => {
      if (url.endsWith('loadUserContent')) return { recordMap: {
        notion_user: { [options.headers['x-notion-active-user-header'] === 'u' ? 'u' : 'other-user']: { value: { name: 'User' } } },
        user_root: { u: { value: { space_view_pointers: ['paid','trial','free','unknown'].map(spaceId => ({ spaceId })) } } },
        space: Object.fromEntries(['paid','trial','free','unknown'].map(id => [id, { value: { id, name: 'Same Name', plan_type: 'team' } }])),
      } };
      if (url.endsWith('getFeatureBillingData')) {
        const { spaceId } = JSON.parse(options.body);
        if (spaceId === 'unknown') throw new Error('unavailable');
        return { billingData: { type: 'public',
          ...(spaceId === 'trial' ? { trial: { endDate: '2099-01-01T00:00:00Z' } } : {}),
          ...(spaceId !== 'free' ? { subscription: { items: [{ price: { product: 'business' } }] } } : {}),
        } };
      }
      return { models: options.headers['x-notion-active-user-header'] === 'u' ? [{ model: 'current-user-model', modelMessage: 'Current model' }] : [] };
    } }),
  });
  await vm.runInContext('extract()', env.context);
  const workspaces = vm.runInContext('extractedWorkspaces', env.context);
  assert.deepEqual(Array.from(workspaces, w => w.business_verified), [true, false, false, false]);
  assert.equal(env.document.getElementById('workspaceSelect').value, 'paid');
  assert.equal(JSON.parse(env.document.getElementById('configJson').value).plan_type, 'business');
  assert.equal(vm.runInContext('extractedConfig.available_models[0]?.id', env.context), 'current-user-model');
});

test('a mismatched API identity is still rejected instead of picking the first returned user', async () => {
  const env = environment();
  Object.assign(env.context, {
    readCurrentNotionSession: async () => ({ tab: { id: 1 }, token: 'fixture', userId: 'current-user' }),
    crypto: { randomUUID: () => 'fixture' },
    chrome: { cookies: { getAll: async () => [] }, scripting: { executeScript: async ({ func, args }) => [{ result: await func(...args) }] } },
    fetch: async () => ({ ok: true, json: async () => ({ recordMap: { notion_user: { 'other-user': { value: { name: 'Other user' } } } } }) }),
  });
  await vm.runInContext('extract()', env.context);
  assert.equal(env.elements.get('status').className, 'status error');
  assert.match(env.elements.get('status').textContent, /用户不匹配/);
  assert.equal(env.elements.get('importBtn').disabled, true);
  assert.equal(vm.runInContext('extractedConfig', env.context), null);
});
