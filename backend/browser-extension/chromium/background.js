// HR Lens: reports the front tab's title and site name (e.g. "github.com") to the HR Lens
// agent on this computer. The full address, page contents and browsing history are never sent,
// and nothing leaves the computer from here: the agent decides what goes to the server.
const AGENT = 'http://127.0.0.1:47123/tab';

async function report(tab) {
  if (!tab || !tab.active || !tab.title) return;
  let host = '';
  try {
    const url = new URL(tab.url || '');
    if (url.protocol === 'http:' || url.protocol === 'https:') host = url.hostname.replace(/^www\./, '');
  } catch {
    // New Tab, settings and other browser pages have no website
  }
  try {
    await fetch(AGENT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: tab.title, host }),
    });
  } catch {
    // The agent is not running: nothing to do
  }
}

async function reportFront() {
  const [tab] = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  report(tab);
}

chrome.tabs.onActivated.addListener(reportFront);
chrome.windows.onFocusChanged.addListener(reportFront);
chrome.tabs.onUpdated.addListener((_id, change, tab) => {
  if (change.title || change.status === 'complete') report(tab);
});
chrome.runtime.onStartup.addListener(reportFront);
chrome.runtime.onInstalled.addListener(reportFront);
