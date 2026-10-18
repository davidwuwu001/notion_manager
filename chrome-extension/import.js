const DASHBOARD_URL = 'http://127.0.0.1:8081/dashboard/';

async function readCurrentNotionSession() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  const url = new URL(tab?.url || 'about:blank');
  if (url.protocol !== 'https:' || !['notion.so', 'notion.com'].some(domain => url.hostname === domain || url.hostname.endsWith('.' + domain))) {
    throw new Error('请先切换到已登录的 Notion 官方页面，再点击扩展。');
  }
  const [token, user] = await Promise.all([
    chrome.cookies.get({ url: tab.url, name: 'token_v2' }),
    chrome.cookies.get({ url: tab.url, name: 'notion_user_id' }),
  ]);
  if (!token?.value || !user?.value) {
    throw new Error('未找到当前 Notion 登录身份，请登录并打开工作区后重试。');
  }
  return { tab, token: token.value, userId: user.value };
}

async function importCurrentAccount() {
  const status = document.getElementById('importStatus');
  const button = document.getElementById('importBtn');
  button.disabled = true;
  document.getElementById('workspaceSelect').disabled = true;
  document.getElementById('extractBtn').disabled = true;
  status.className = 'status info';
  status.textContent = '正在读取当前账号并连接本机管理面板…';
  let dashboard;
  try {
    const config = getSelectedAccountConfig();
    if (!config) throw new Error('请先读取账号，并选择已确认的正式 Business 工作区。');
    const session = await readCurrentNotionSession();
    if (config.user_id !== session.userId || config.token_v2 !== session.token) {
      throw new Error('浏览器登录身份已变化，请重新读取账号与工作区后导入。');
    }
    const tabs = await chrome.tabs.query({ url: DASHBOARD_URL + '*' });
    dashboard = tabs[0] || await chrome.tabs.create({ url: DASHBOARD_URL, active: false });
    // Wait for a new local tab without moving focus away from this popup.
    for (let attempt = 0; attempt < 100; attempt++) {
      const current = await chrome.tabs.get(dashboard.id);
      if (current.status === 'complete') break;
      if (attempt === 99) throw new Error('管理面板加载超时，请确认本机服务已启动。');
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    const [{ result }] = await chrome.scripting.executeScript({
      target: { tabId: dashboard.id },
      func: async (token, userId, spaceId) => {
        if (location.origin !== 'http://127.0.0.1:8081') return { error: '管理面板地址不正确。' };
        try {
          const response = await fetch('/admin/accounts/add', {
            method: 'POST', credentials: 'same-origin',
            headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
            body: JSON.stringify({ token_v2: token, notion_user_id: userId, space_id: spaceId }),
          });
          if (response.status === 401) return { loginRequired: true };
          const data = await response.json();
          if (!response.ok || data.status !== 'ok') return { error: data.error || `HTTP ${response.status}` };
          window.dispatchEvent(new Event('notion-manager-account-imported'));
          return { account: data.account };
        } catch {
          return { error: '导入结果暂时无法确认，请先查看账号池是否已添加，再决定是否重试。' };
        }
      },
      args: [session.token, session.userId, config.space_id],
    });
    if (result?.loginRequired) {
      status.className = 'status error';
      status.textContent = '请先登录控制面板，然后切回 Notion，再点一次导入。';
      document.getElementById('openDashboardBtn').hidden = false;
      return;
    }
    if (!result?.account) throw new Error(result?.error || '未收到导入结果，请先查看账号池。');
    status.className = 'status success';
    status.textContent = `已导入：${result.account.name}（${result.account.email}）\n工作区：${result.account.space}\nID：${result.account.space_id || config.space_id}`;
    document.getElementById('openDashboardBtn').hidden = false;
  } catch (error) {
    status.className = 'status error';
    status.textContent = error.message || '导入失败，请检查本机服务。';
  } finally {
    button.disabled = !getSelectedAccountConfig();
    document.getElementById('workspaceSelect').disabled = false;
    document.getElementById('extractBtn').disabled = false;
  }
}

document.getElementById('importBtn').addEventListener('click', importCurrentAccount);
document.getElementById('openDashboardBtn').addEventListener('click', async () => {
  const tabs = await chrome.tabs.query({ url: DASHBOARD_URL + '*' });
  if (tabs[0]) await chrome.tabs.update(tabs[0].id, { active: true });
  else await chrome.tabs.create({ url: DASHBOARD_URL });
});
