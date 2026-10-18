const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function environment({ url = 'https://app.notion.com/ai', status = 200, invalidSession = false, selected = true, staleSession = false } = {}) {
  const elements = new Map();
  const calls = [];
  const events = [];
  const document = { getElementById(id) {
    if (!elements.has(id)) elements.set(id, { hidden: true, addEventListener() {} });
    return elements.get(id);
  } };
  const sandbox = {
    getSelectedAccountConfig: () => selected ? { token_v2: staleSession ? 'old-token' : 'fixture-secret-token', user_id: 'current-user', space_id: 'selected-business-space' } : null,
    URL, document, setTimeout, location: { origin: 'http://127.0.0.1:8081' },
    Event: class { constructor(type) { this.type = type; } },
    window: { dispatchEvent: event => events.push(event.type) },
    chrome: {
      tabs: {
        query: async options => options.active ? [{ id: 1, url }] : [{ id: 2, status: 'complete' }],
        get: async () => ({ status: 'complete' }),
      },
      cookies: { get: async ({ name }) => invalidSession ? null : { value: name === 'token_v2' ? 'fixture-secret-token' : 'current-user' } },
      scripting: { executeScript: async ({ func, args }) => [{ result: await func(...args) }] },
    },
    fetch: async (path, options) => {
      calls.push({ path, options });
      return { status, ok: status === 200, json: async () => ({ status: 'ok', account: { name: 'Test', email: 'test@example.com', space: 'Test Space' } }) };
    },
  };
  const context = vm.createContext(sandbox);
  vm.runInContext(fs.readFileSync(require.resolve('./import.js'), 'utf8'), context);
  return { context, elements, calls, events };
}

test('imports selected browser identity through authenticated same-origin dashboard request', async () => {
  const env = environment();
  await vm.runInContext('importCurrentAccount()', env.context);
  assert.equal(env.calls.length, 1);
  assert.equal(env.calls[0].path, '/admin/accounts/add');
  assert.equal(env.calls[0].options.credentials, 'same-origin');
  assert.deepEqual(JSON.parse(env.calls[0].options.body), { token_v2: 'fixture-secret-token', notion_user_id: 'current-user', space_id: 'selected-business-space' });
  assert.deepEqual(env.events, ['notion-manager-account-imported']);
  assert.match(env.elements.get('importStatus').textContent, /已导入/);
  assert.equal(env.elements.get('importStatus').textContent.includes('fixture-secret-token'), false);
});

test('missing verified workspace and changed login never send an import request', async () => {
  for (const options of [{ selected: false }, { staleSession: true }]) {
    const env = environment(options);
    await vm.runInContext('importCurrentAccount()', env.context);
    assert.equal(env.calls.length, 0);
    assert.equal(env.elements.get('importStatus').className, 'status error');
  }
});

test('expired dashboard login asks for login and never retries import automatically', async () => {
  const env = environment({ status: 401 });
  await vm.runInContext('importCurrentAccount()', env.context);
  assert.equal(env.calls.length, 1);
  assert.match(env.elements.get('importStatus').textContent, /先登录/);
  assert.equal(env.elements.get('openDashboardBtn').hidden, false);
  assert.equal(env.events.length, 0);
});

test('rejects lookalike Notion domains and missing current user before sending credentials', async () => {
  for (const options of [{ url: 'https://app.notion.com.attacker.example/ai' }, { invalidSession: true }]) {
    const env = environment(options);
    await vm.runInContext('importCurrentAccount()', env.context);
    assert.equal(env.calls.length, 0);
    assert.equal(env.elements.get('importStatus').className, 'status error');
    assert.equal(env.elements.get('importBtn').disabled, false);
  }
});

test('supports both official Notion domains', async () => {
  for (const url of ['https://www.notion.so/ai', 'https://app.notion.com/ai']) {
    const env = environment({ url });
    await vm.runInContext('importCurrentAccount()', env.context);
    assert.equal(env.calls.length, 1);
  }
});
