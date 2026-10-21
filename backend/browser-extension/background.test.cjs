const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function extension(os, failNative = false) {
  const native = [], http = [];
  const event = { addListener() {} };
  const context = vm.createContext({
    URL,
    chrome: {
      runtime: { getPlatformInfo: async () => ({ os }), sendNativeMessage: async (...args) => {
        native.push(args); if (failNative) throw new Error('host unavailable'); return { ok: true };
      }, onStartup: event, onInstalled: event },
      tabs: { query: async () => [], onActivated: event, onUpdated: event },
      windows: { onFocusChanged: event },
      alarms: { create() {}, onAlarm: event },
    },
    fetch: async (...args) => { http.push(args); },
    tab: { active: true, title: 'Synthetic tab', url: 'https://www.example.com/private?q=secret' },
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, 'chromium/background.js'), 'utf8'), context);
  return { native, http, report: () => vm.runInContext('report(tab)', context) };
}

test('Windows sends only title/domain over native messaging', async () => {
  const e = extension('win'); await e.report();
  assert.equal(e.http.length, 0);
  assert.equal(e.native[0][0], 'com.hrlens.agent');
  assert.equal(JSON.stringify(e.native[0][1]), JSON.stringify({ title: 'Synthetic tab', host: 'example.com' }));
});
test('Windows never falls back to the shared Linux port', async () => {
  const e = extension('win', true); await e.report();
  assert.equal(e.native.length, 1); assert.equal(e.http.length, 0);
});
test('Linux keeps its existing HTTP transport', async () => {
  const e = extension('linux'); await e.report();
  assert.equal(e.native.length, 0); assert.equal(e.http.length, 1);
  assert.equal(JSON.parse(e.http[0][1].body).host, 'example.com');
});
