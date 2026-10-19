const STRINGS = {
  zh: {
    subtitle: '一键提取账号配置，用于 Notion AI Proxy',
    statusInitial: '读取当前登录账号，选择正式 Business 工作区，即可直接导入本机账号池。无需导出文件。',
    extractBtn: '读取当前账号与工作区',
    copyBtn: '📋 复制 JSON 配置',
    extracting: '<span class="spinner"></span>正在提取配置...',
    noCookies: '未找到 notion.so cookies。请先登录 Notion。',
    noToken: '未找到 token_v2 cookie。请先登录 Notion。',
    cookiesSuccess: '<span class="spinner"></span>已获取 cookies，正在获取用户数据...',
    openNotionFirst: '请先打开 Notion 页面 (notion.so)，然后重试。',
    noWorkspace: '未找到可用空间',
    extractFailed: '提取失败',
    success: '✅ 配置提取成功！',
    copied: '✅ 已复制！',
    labelUser: '用户',
    labelWorkspace: '空间',
    labelTimezone: '时区',
    labelVersion: '版本',
    labelToken: 'Token'
  },
  en: {
    subtitle: 'One-click account config extractor for Notion AI Proxy',
    statusInitial: 'Read your signed-in account, select a verified Business workspace, and import directly into the local pool.',
    extractBtn: 'Read account and workspaces',
    copyBtn: '📋 Copy JSON Config',
    extracting: '<span class="spinner"></span>Extracting configuration...',
    noCookies: 'No notion.so cookies found. Please log in to Notion first.',
    noToken: 'No token_v2 cookie found. Please log in to Notion first.',
    cookiesSuccess: '<span class="spinner"></span>Cookies retrieved, fetching user data...',
    openNotionFirst: 'Please open the Notion page (notion.so) first, then try again.',
    noWorkspace: 'No accessible workspace found',
    extractFailed: 'Extraction failed',
    success: '✅ Configuration extracted successfully!',
    copied: '✅ Copied!',
    labelUser: 'User',
    labelWorkspace: 'Workspace',
    labelTimezone: 'Timezone',
    labelVersion: 'Version',
    labelToken: 'Token'
  }
};

let currentLang = localStorage.getItem('notion-ext-lang') || 'zh';
let extractedConfig = null;
let extractedWorkspaces = [];

function updateUI() {
  const s = STRINGS[currentLang];
  document.getElementById('subtitle').textContent = s.subtitle;
  const statusEl = document.getElementById('status');
  if (statusEl.className === 'status info') {
    statusEl.textContent = s.statusInitial;
  }
  document.getElementById('extractBtn').textContent = s.extractBtn;
  document.getElementById('copyBtn').textContent = s.copyBtn;
  document.getElementById('exportBtn').textContent = currentLang === 'zh' ? '下载账号 JSON 文件' : 'Download account JSON';
  document.getElementById('langToggleBtn').textContent = currentLang === 'zh' ? '🌐 EN' : '🌐 中文';
}

async function extract() {
  const statusEl = document.getElementById('status');
  const extractBtn = document.getElementById('extractBtn');
  const resultEl = document.getElementById('result');
  const s = STRINGS[currentLang];

  extractBtn.disabled = true;
  extractedConfig = null;
  document.getElementById('importBtn').disabled = true;
  resultEl.classList.add('hidden');
  statusEl.className = 'status loading';
  statusEl.innerHTML = s.extracting;

  try {
    const session = await readCurrentNotionSession();
    const allCookies = await chrome.cookies.getAll({ url: session.tab.url });

    const cookieMap = {};
    for (const c of allCookies) cookieMap[c.name] = c.value;

    const token = session.token;
    if (!token) throw new Error(s.noToken);

    const browserId = cookieMap['notion_browser_id'] || '';
    const deviceId = cookieMap['device_id'] || '';
    const fullCookie = allCookies.map(c => `${c.name}=${c.value}`).join('; ');

    // Generate a browser_id if cookie doesn't exist
    const effectiveBrowserId = browserId || crypto.randomUUID();

    statusEl.innerHTML = s.cookiesSuccess;

    // Step 2: Get active tab and inject script to call Notion APIs
    const tab = session.tab;

    // Step 3: Inject content script to call APIs with credentials
    const [{ result: accountData }] = await chrome.scripting.executeScript({
      target: { tabId: tab.id },
      args: [session.userId],
      func: async (activeUserId) => {
        try {
          // Call loadUserContent
          const userResp = await fetch('/api/v3/loadUserContent', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'x-notion-active-user-header': activeUserId },
            credentials: 'include',
            body: '{}'
          });
          if (!userResp.ok) throw new Error(`API error: ${userResp.status}`);
          const userData = await userResp.json();
          const rm = userData.recordMap;

          const getVal = (record) => record?.value?.value || record?.value;

          const userId = activeUserId;
          if (!rm.notion_user?.[userId]) throw new Error('当前登录身份与 Notion 返回的用户不匹配');
          const user = getVal(rm.notion_user[userId]);
          const userRoot = getVal(rm.user_root[userId]);
          const spacePointers = userRoot.space_view_pointers || [];

          // Find best space (AI enabled, non-free preferred)
          let bestSpace = null;
          const workspaces = [];
          for (const ptr of spacePointers) {
            const spaceData = getVal(rm.space?.[ptr.spaceId]);
            if (spaceData) {
              const workspace = { id: spaceData.id || ptr.spaceId, name: spaceData.name || ptr.spaceId, plan_type: 'unknown', business_verified: false, space_view_id: ptr.id };
              try {
                const response = await fetch('/api/v3/getFeatureBillingData', {
                  method: 'POST', credentials: 'include',
                  headers: { 'Content-Type': 'application/json', 'x-notion-active-user-header': activeUserId },
                  body: JSON.stringify({ spaceId: workspace.id }),
                });
                if (!response.ok) throw new Error('subscription check failed');
                const { billingData: billing } = await response.json();
                if (!billing || !['public', 'admin'].includes(billing.type)) throw new Error('unknown subscription');
                const subscription = billing.subscription;
                const trialEnd = subscription?.trialEnd;
                const trial = billing.trial != null || (trialEnd != null && (!Number.isFinite(Date.parse(trialEnd)) || Date.parse(trialEnd) > Date.now()));
                const products = (subscription?.items || []).map(item => item.price?.product);
                workspace.plan_type = trial ? 'Business Trial（不可导入）' : products.find(p => ['business', 'enterprise', 'plus'].includes(p)) || 'free';
                workspace.business_verified = !trial && products.includes('business');
              } catch {
                workspace.plan_type = '未确认订阅（不可导入）';
              }
              workspaces.push(workspace);
              const aiEnabled = spaceData.settings?.enable_ai_feature !== false &&
                                spaceData.settings?.disable_ai_feature !== true;
              if (!bestSpace || (aiEnabled && spaceData.plan_type !== 'free')) {
                bestSpace = { ...spaceData, spaceViewId: ptr.id };
              }
            }
          }

          if (!bestSpace) throw new Error('未找到可用空间');

          const userSettings = getVal(rm.user_settings?.[userId]);
          const settings = userSettings?.settings || {};
          const timezone = settings.time_zone || Intl.DateTimeFormat().resolvedOptions().timeZone;

          // Get available models
          const modelResp = await fetch('/api/v3/getAvailableModels', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'x-notion-active-user-header': activeUserId },
            credentials: 'include',
            body: JSON.stringify({ spaceId: bestSpace.id })
          });
          let models = [];
          if (modelResp.ok) {
            const modelData = await modelResp.json();
            models = (modelData.models || [])
              .filter(m => !m.isDisabled)
              .map(m => ({ name: m.modelMessage, id: m.model }));
          }

          // Get client version — wait up to 5s for window.CONFIG to initialize
          let clientVersion = window.CONFIG?.version;
          if (!clientVersion) {
            for (let i = 0; i < 50; i++) {
              await new Promise(r => setTimeout(r, 100));
              clientVersion = window.CONFIG?.version;
              if (clientVersion) break;
            }
          }
          // Fallback: extract from notion-client-version in performance entries
          if (!clientVersion) {
            try {
              const entries = performance.getEntriesByType('resource');
              for (const e of entries) {
                if (e.name.includes('/api/v3/')) {
                  // Try to find notion-client-version from XHR headers via serverTiming
                  break;
                }
              }
            } catch(e) {}
          }
          // Fallback: extract from document scripts version pattern
          if (!clientVersion) {
            const scriptEls = document.querySelectorAll('script[src*="notion.so/_assets/app-"]');
            if (scriptEls.length > 0) {
              // Use a sensible default based on today's date
              const d = new Date();
              const yy = d.getFullYear();
              const mm = String(d.getMonth()+1).padStart(2,'0');
              const dd = String(d.getDate()).padStart(2,'0');
              clientVersion = `23.13.${yy}${mm}${dd}.0000`;
            }
          }
          if (!clientVersion) clientVersion = 'unknown';

          return {
            success: true,
            user_id: userId,
            user_name: user.name,
            user_email: user.email,
            space_id: bestSpace.id,
            space_name: bestSpace.name,
            space_view_id: bestSpace.spaceViewId,
            plan_type: bestSpace.plan_type,
            timezone: timezone,
            client_version: clientVersion,
            available_models: models,
            workspaces
          };
        } catch (err) {
          return { success: false, error: err.message };
        }
      }
    });

    if (!accountData?.success) {
      throw new Error(accountData?.error || s.extractFailed);
    }

    // Step 4: Assemble final config
    const config = {
      token_v2: token,
      user_id: accountData.user_id,
      user_name: accountData.user_name,
      user_email: accountData.user_email,
      space_id: accountData.space_id,
      space_name: accountData.space_name,
      space_view_id: accountData.space_view_id,
      plan_type: accountData.plan_type,
      timezone: accountData.timezone,
      client_version: accountData.client_version,
      browser_id: effectiveBrowserId,
      device_id: deviceId,
      full_cookie: fullCookie,
      available_models: accountData.available_models,
      extracted_at: new Date().toISOString()
    };

    // Step 5: Display results
    statusEl.className = 'status success';
    statusEl.textContent = s.success;

    const infoRows = document.getElementById('infoRows');
    infoRows.replaceChildren();
    for (const [label, value] of [
      [s.labelUser, `${config.user_name} (${config.user_email})`],
      [s.labelTimezone, config.timezone],
      [s.labelVersion, config.client_version],
    ]) {
      const row = document.createElement('div');
      row.className = 'info-row';
      for (const [className, text] of [['info-label', label], ['info-value', value]]) {
        const span = document.createElement('span');
        span.className = className;
        span.textContent = text;
        row.appendChild(span);
      }
      infoRows.appendChild(row);
    }

    const modelTags = document.getElementById('modelTags');
    modelTags.replaceChildren();
    for (const model of config.available_models) {
      const span = document.createElement('span');
      span.className = 'model-tag';
      span.textContent = model.name;
      modelTags.appendChild(span);
    }

    extractedConfig = config;
    extractedWorkspaces = accountData.workspaces;
    const select = document.getElementById('workspaceSelect');
    select.replaceChildren();
    const placeholder = document.createElement('option');
    placeholder.value = '';
    placeholder.textContent = currentLang === 'zh' ? '请选择工作区（按 ID 区分同名工作区）' : 'Select workspace (check its ID)';
    select.appendChild(placeholder);
    for (const workspace of extractedWorkspaces) {
      const option = document.createElement('option');
      option.value = workspace.id;
      option.textContent = `${workspace.name} (${workspace.business_verified ? 'Business Plan' : workspace.plan_type}) · ${workspace.id}`;
      option.disabled = !workspace.business_verified;
      select.appendChild(option);
    }
    const allowed = extractedWorkspaces.filter(workspace => workspace.business_verified);
    select.value = allowed.length === 1 ? allowed[0].id : '';
    if (!allowed.length) {
      statusEl.className = 'status error';
      statusEl.textContent = '未找到可确认的正式 Business Plan；试用、Free 和订阅不明的工作区不能导出。';
    }
    selectWorkspace();
    resultEl.classList.remove('hidden');

  } catch (err) {
    statusEl.className = 'status error';
    statusEl.textContent = '❌ ' + err.message;
  } finally {
    extractBtn.disabled = false;
  }
}

function selectWorkspace() {
  const id = document.getElementById('workspaceSelect').value;
  const workspace = extractedWorkspaces.find(item => item.id === id && item.business_verified);
  document.getElementById('exportBtn').disabled = !workspace;
  document.getElementById('copyBtn').disabled = !workspace;
  document.getElementById('importBtn').disabled = !workspace;
  document.getElementById('importStatus').className = 'status info';
  document.getElementById('importStatus').textContent = '仅导入所选工作区；后端将再次核实正式 Business 订阅。';
  const tags = document.getElementById('modelTags');
  tags.replaceChildren();
  if (workspace && workspace.id === extractedConfig?.space_id) {
    for (const model of extractedConfig.available_models || []) {
      const span = document.createElement('span');
      span.className = 'model-tag';
      span.textContent = model.name;
      tags.appendChild(span);
    }
  }
  const config = getSelectedAccountConfig();
  document.getElementById('configJson').value = config ? JSON.stringify(config, null, 2) : '';
}

function getSelectedAccountConfig() {
  const id = document.getElementById('workspaceSelect').value;
  const workspace = extractedWorkspaces.find(item => item.id === id && item.business_verified);
  return workspace && extractedConfig ? {
    token_v2: extractedConfig.token_v2,
    user_id: extractedConfig.user_id,
    user_name: extractedConfig.user_name,
    user_email: extractedConfig.user_email,
    space_id: workspace.id,
    space_name: workspace.name,
    plan_type: workspace.plan_type,
    extracted_at: new Date().toISOString(),
  } : null;
}

function exportConfig() {
  const text = document.getElementById('configJson').value;
  if (!text) return;
  const config = JSON.parse(text);
  const blob = new Blob([text], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = `notion-account-${config.space_id.replace(/[^a-zA-Z0-9-]/g, '_')}.json`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

async function copyConfig() {
  const textarea = document.getElementById('configJson');
  const btn = document.getElementById('copyBtn');
  try {
    await navigator.clipboard.writeText(textarea.value);
  } catch {
    btn.textContent = currentLang === 'zh' ? '复制失败，请下载 JSON 文件' : 'Copy failed; download JSON instead';
    return;
  }
  btn.textContent = STRINGS[currentLang].copied;
  btn.classList.add('copied');
  setTimeout(() => {
    btn.textContent = STRINGS[currentLang].copyBtn;
    btn.classList.remove('copied');
  }, 2000);
}

document.getElementById('extractBtn').addEventListener('click', extract);
document.getElementById('copyBtn').addEventListener('click', copyConfig);
document.getElementById('exportBtn').addEventListener('click', exportConfig);
document.getElementById('workspaceSelect').addEventListener('change', selectWorkspace);
document.getElementById('langToggleBtn').addEventListener('click', () => {
  currentLang = currentLang === 'zh' ? 'en' : 'zh';
  localStorage.setItem('notion-ext-lang', currentLang);
  updateUI();
});

// Call updateUI on load
document.addEventListener('DOMContentLoaded', updateUI);
