// web/static/app.js

let environments = [];
let activeSessions = new Map();
let currentEnvId = null;
let availableK8sVersions = []; // ★ 利用可能なK8sバージョンを保持する配列
let currentStatusFilter = 'all'; // ★ フィルタの現在の状態を保持する変数を追加

// K8s version loading optimization
let k8sVersionsCache = {
    data: null,
    timestamp: null,
    ttl: 5 * 60 * 1000 // 5 minutes cache TTL
};
let k8sVersionsLoadingPromise = null;

const appLayout = document.getElementById('appLayout');
const sidebar = document.getElementById('sidebar');
const mainPanel = document.getElementById('mainPanel');

document.addEventListener('DOMContentLoaded', async function() {
    loadEnvironments(); // 認証後にバージョンも読み込まれるように修正（commit b4b4350 fix maintained）
    setInterval(loadEnvironments, 5000);
    
    // サイドバーのクリックイベントリスナーを追加
    if (sidebar) {
        sidebar.addEventListener('click', handleSidebarClick);
    }
    
    window.addEventListener('beforeunload', () => {
        activeSessions.forEach((session, envId) => {
            disconnectTerminal(envId, false);
        });
        activeSessions.clear();
    });
});

// ★ filterEnvironments 関数をグローバルスコープで定義
function filterEnvironments() {
    const filterSelect = document.getElementById('status-filter');
    if (filterSelect) {
        currentStatusFilter = filterSelect.value;
        renderUIBasedOnState(); // フィルタが変更されたらUIを再描画
    }
}


// ★ 利用可能なK8sバージョンを取得し、ドロップダウンを更新する関数（最適化版）
async function loadAvailableK8sVersions(forceRefresh = false) {
    // Return existing promise if already loading
    if (k8sVersionsLoadingPromise) {
        return k8sVersionsLoadingPromise;
    }
    
    // Check cache first (unless force refresh or first load)
    if (!forceRefresh && k8sVersionsCache.data && k8sVersionsCache.timestamp && k8sVersionsCache.data.length > 0) {
        const cacheAge = Date.now() - k8sVersionsCache.timestamp;
        if (cacheAge < k8sVersionsCache.ttl) {
            console.log('Using cached K8s versions:', k8sVersionsCache.data);
            availableK8sVersions = k8sVersionsCache.data;
            populateK8sVersionDropdowns(availableK8sVersions);
            return Promise.resolve();
        }
    }
    
    // Create loading promise
    k8sVersionsLoadingPromise = loadK8sVersionsWithRetry();
    
    try {
        await k8sVersionsLoadingPromise;
    } finally {
        k8sVersionsLoadingPromise = null;
    }
}

// Helper function to load K8s versions with retry logic
async function loadK8sVersionsWithRetry(maxRetries = 3, retryDelay = 1000) {
    let lastError = null;
    
    for (let attempt = 1; attempt <= maxRetries; attempt++) {
        try {
            const response = await fetch('/api/k8s-versions', {
                headers: {
                    'Cache-Control': 'no-cache'
                }
            });
            
            if (!response.ok) {
                throw new Error(`Server responded with status: ${response.status}`);
            }
            
            const data = await response.json();
            const versions = data.versions || [];
            
            // 自然順ソート (例: "1.28", "1.9" -> "1.9", "1.28")
            versions.sort((a, b) => {
                const partsA = a.split('.').map(Number);
                const partsB = b.split('.').map(Number);
                for (let i = 0; i < Math.max(partsA.length, partsB.length); i++) {
                    const valA = partsA[i] || 0;
                    const valB = partsB[i] || 0;
                    if (valA < valB) return -1;
                    if (valA > valB) return 1;
                }
                return 0;
            }).reverse(); // 新しいバージョンを上に表示するため逆順にする

            // Update cache
            k8sVersionsCache.data = versions;
            k8sVersionsCache.timestamp = Date.now();
            
            // Update global variable
            availableK8sVersions = versions;
            populateK8sVersionDropdowns(availableK8sVersions);
            
            console.log(`K8s versions loaded successfully on attempt ${attempt}:`, versions);
            return;
            
        } catch (error) {
            lastError = error;
            console.warn(`Failed to load K8s versions (attempt ${attempt}/${maxRetries}):`, error.message);
            
            if (attempt < maxRetries) {
                // Wait before retrying, with exponential backoff
                const delay = retryDelay * Math.pow(2, attempt - 1);
                console.log(`Retrying in ${delay}ms...`);
                await new Promise(resolve => setTimeout(resolve, delay));
            }
        }
    }
    
    // All retries failed
    console.error('Failed to load K8s versions after all retries:', lastError);
    
    // Use cached data if available, otherwise try fallback values
    if (k8sVersionsCache.data && k8sVersionsCache.data.length > 0) {
        console.log('Using cached K8s versions due to loading failure');
        availableK8sVersions = k8sVersionsCache.data;
        populateK8sVersionDropdowns(availableK8sVersions);
    } else {
        console.log('No cached data available, using fallback versions');
        // Fallback to default versions if no cache and all requests failed
        const fallbackVersions = ['1.33', '1.32', '1.31', '1.30'];
        availableK8sVersions = fallbackVersions;
        populateK8sVersionDropdowns(availableK8sVersions);
    }
}

// Manual refresh function for debugging (can be called from browser console)
async function debugRefreshK8sVersions() {
    console.log('Debug: Forcing refresh of K8s versions...');
    k8sVersionsCache.data = null;
    k8sVersionsCache.timestamp = null;
    await loadAvailableK8sVersions(true);
}

// ★ K8sバージョンドロップダウンを動的に生成する関数
function populateK8sVersionDropdowns(versions) {
    console.log('populateK8sVersionDropdowns called with versions:', versions);
    const sidebarSelect = document.getElementById('k8s-version');
    const mainPanelSelect = document.getElementById('k8s-version-main'); // no-env-container 内のセレクトボックス

    function updateSelect(selectElement) {
        if (!selectElement) {
            console.log('Select element not found');
            return;
        }
        
        // 現在選択されている値を保存
        const currentValue = selectElement.value;
        console.log('Current selected value:', currentValue);
        
        selectElement.innerHTML = ''; // 既存のオプションをクリア

        if (!versions || versions.length === 0) {
            console.log('No versions available, creating placeholder option');
            const option = document.createElement('option');
            option.value = "";
            option.textContent = "Loading versions...";
            selectElement.appendChild(option);
            selectElement.disabled = true;
        } else {
            console.log('Populating with versions:', versions);
            selectElement.disabled = false;
            versions.forEach((version, index) => {
                const option = document.createElement('option');
                option.value = version;
                option.textContent = `v${version}${index === 0 ? ' (Latest)' : ''}`; // 最新バージョンにラベル付け
                selectElement.appendChild(option);
            });
            selectElement.disabled = false;
            
            // 以前の選択値を復元（存在する場合）
            if (currentValue && versions.includes(currentValue)) {
                selectElement.value = currentValue;
                console.log('Restored previous selection:', currentValue);
            }
        }
    }

    updateSelect(sidebarSelect);
    updateSelect(mainPanelSelect);
}


async function loadEnvironments() {
    try {
        const response = await fetch('/api/environments');
        if (!response.ok) {
            console.error('Failed to load environments, server responded with status:', response.status);
            renderUIBasedOnState();
            return;
        }
        const data = await response.json();
        const newEnvIds = new Set((data.environments || []).map(env => env.id));

        activeSessions.forEach((session, envId) => {
            if (!newEnvIds.has(envId)) {
                console.log(`Environment ${envId} no longer exists on server. Cleaning up local session.`);
                disconnectTerminal(envId, currentEnvId === envId);
            }
        });
        
        environments = data.environments || [];
        environments.sort((a, b) => (a.display_name || a.id || "").localeCompare(b.display_name || b.id || ""));
        
        // 認証が成功している場合のみKubernetesバージョンを読み込む
        if (availableK8sVersions.length === 0) {
            await loadAvailableK8sVersions();
        }
        
        renderUIBasedOnState();

    } catch (error) {
        console.error('Failed to load environments:', error);
        renderUIBasedOnState();
    }
}

function renderUIBasedOnState() {
    if (!appLayout || !mainPanel) return;

    const envListInSidebar = document.getElementById('env-list');

    if (environments.length === 0 && activeSessions.size === 0) {
        renderNoEnvironmentsView();
        if (envListInSidebar) envListInSidebar.innerHTML = '';
        currentEnvId = null;
        appLayout.classList.remove('sidebar-collapsed');
    } else {
        appLayout.classList.remove('no-environments');
        renderSidebarContent();

        // Check if we're currently showing terminal, browser panel, or split view
        const hasTerminalPanel = document.getElementById('terminal-panel');
        const hasBrowserPanel = document.getElementById('browser-panel');
        const hasSplitView = document.getElementById('split-view');
        
        if (currentEnvId && (activeSessions.has(currentEnvId) && hasTerminalPanel || hasBrowserPanel || hasSplitView)) {
            appLayout.classList.add('terminal-active');
        } else {
            appLayout.classList.remove('terminal-active');
            appLayout.classList.remove('sidebar-collapsed');
            if (!mainPanel.hasChildNodes() || mainPanel.querySelector('.no-env-container') || (currentEnvId && !hasTerminalPanel && !hasBrowserPanel && !hasSplitView)) {
                 mainPanel.innerHTML = '<div style="text-align:center; color: #777; margin-top: 50px; padding:2rem;">Select an environment to connect or create a new one.</div>';
            }
        }
    }
     // 利用可能なバージョンがない場合、作成ボタンを無効化する（オプション）
    const createButtons = document.querySelectorAll('.btn[onclick^="createEnvironment"]');
    createButtons.forEach(button => {
        button.disabled = availableK8sVersions.length === 0;
        if (availableK8sVersions.length === 0) {
            button.title = "No Kubernetes versions available to create an environment.";
        } else {
            button.title = "";
        }
    });
}

function renderNoEnvironmentsView() {
    if (!mainPanel || !appLayout) return;

    appLayout.classList.add('no-environments');
    appLayout.classList.remove('sidebar-collapsed', 'terminal-active');

    // no-env-containerが既に存在する場合は、入力値を保持する
    const existingContainer = mainPanel.querySelector('.no-env-container');
    let preservedEnvName = '';
    let preservedK8sVersion = '';
    
    if (existingContainer) {
        const envNameInput = existingContainer.querySelector('#env-name-main');
        const k8sVersionSelect = existingContainer.querySelector('#k8s-version-main');
        
        if (envNameInput) preservedEnvName = envNameInput.value;
        if (k8sVersionSelect) preservedK8sVersion = k8sVersionSelect.value;
    }

    // 既存のno-env-containerがある場合は何もせず、ない場合のみ新規作成
    if (!existingContainer) {
        mainPanel.innerHTML = `
            <div class="no-env-container">
                <div class="actions">
                    <h2>Create New Environment</h2>
                    <div class="form-row">
                        <div class="form-group full-width">
                            <label for="env-name-main">Environment Name (Optional)</label>
                            <input type="text" id="env-name-main" placeholder="e.g., My Test Cluster" value="${preservedEnvName}">
                        </div>
                    </div>
                    <div class="form-row">
                        <div class="form-group">
                            <label for="k8s-version-main">Kubernetes Version</label>
                            <select id="k8s-version-main">
                                 <option value="">Loading versions...</option>
                            </select>
                        </div>
                        <button class="btn" onclick="createEnvironmentFromMainPanel()">Create</button>
                    </div>
                </div>
                <div class="empty-message">No environments yet. Create your first one using the form above!</div>
            </div>
        `;
        populateK8sVersionDropdowns(availableK8sVersions); // ★ no-envビューのドロップダウンも更新
        
        // 保存された値を復元
        if (preservedEnvName || preservedK8sVersion) {
            const envNameInput = document.getElementById('env-name-main');
            const k8sVersionSelect = document.getElementById('k8s-version-main');
            
            if (envNameInput && preservedEnvName) envNameInput.value = preservedEnvName;
            if (k8sVersionSelect && preservedK8sVersion) k8sVersionSelect.value = preservedK8sVersion;
        }
    } else {
        // 既存のコンテナがある場合は、バージョンが変更された場合のみドロップダウンを更新
        const k8sVersionSelect = existingContainer.querySelector('#k8s-version-main');
        if (k8sVersionSelect && k8sVersionSelect.options.length <= 1) {
            // オプションが1個以下（初期化状態または空）の場合のみ更新
            populateK8sVersionDropdowns(availableK8sVersions);
        }
    }
}

function renderSidebarContent() {
    const envList = document.getElementById('env-list');
    if (!envList) return;

    // ★ フィルタリング処理
    const filteredEnvs = environments.filter(env => {
        if (env.status === 'terminated') {
            return false; // Terminated状態のものは常に非表示
        }
        if (currentStatusFilter === 'all') {
            return true;
        }
        return env.status === currentStatusFilter;
    });

    if (filteredEnvs.length === 0) {
        if (currentStatusFilter === 'all' && environments.length === 0) {
            envList.innerHTML = '<div class="empty">No environments yet. Create your first one!</div>';
        } else {
            envList.innerHTML = `<div class="empty">No environments with status: ${currentStatusFilter}.</div>`;
        }
        return;
    }

    envList.innerHTML = filteredEnvs.map(env => {
        let buttonHtml = '';
        const session = activeSessions.get(env.id);
        const isSessionConnecting = session && session.isConnecting;
        const isSessionActiveAndConnected = session && session.term && !session.term.isDisposed && session.socket && session.socket.readyState === WebSocket.OPEN;

        let itemClass = 'env-item';
        if (isSessionActiveAndConnected) {
            if (currentEnvId === env.id) {
                itemClass += ' env-item-active connected'; 
            } else {
                itemClass += ' env-item-connected-background connected'; 
            }
        }

        let showActionButtons = false;
        let statusLabelHtml = `<div class="env-status status-${env.status || 'unknown'}">${env.status || 'unknown'}</div>`;
        const displayName = env.display_name || `Environment ${env.id.substring(0, 8)}`;

        switch (env.status) {
            case 'available':
                showActionButtons = true;
                if (isSessionConnecting) {
                    buttonHtml = `<button class="btn btn-warning btn-sm" disabled>Connecting...</button>`;
                } else if (isSessionActiveAndConnected) {
                    buttonHtml = `<button class="btn btn-success btn-sm" onclick="showTerminalForEnv('${env.id}')">
                                    ${currentEnvId === env.id ? 'Connected' : 'Show Terminal'}
                                  </button>`;
                } else { 
                    buttonHtml = `<button class="btn btn-primary btn-sm" onclick="connectEnvironment('${env.id}')">Terminal</button>`;
                }
                buttonHtml += ` <button class="btn btn-info btn-sm" onclick="showBrowserTab('${env.id}')" title="Open split view with browser">Browser</button>`;
                buttonHtml += ` <button class="btn btn-danger btn-sm" onclick="destroyEnvironment('${env.id}')">Destroy</button>`;
                break;
            case 'pending':
            case 'generating':
                itemClass += ' env-item-pending'; 
                break;
            case 'error':
                itemClass += ' env-item-error';
                showActionButtons = true;
                buttonHtml = `<button class="btn btn-danger btn-sm" onclick="destroyEnvironment('${env.id}')">Destroy</button>`;
                break;
            case 'shutdown':
            case 'terminated':
                itemClass += ' env-item-terminated';
                break;
            default:
        }

        return `
        <div class="${itemClass}" data-env-id="${env.id}">
            <div class="env-item-header">
                <div class="env-name-container">
                    <span class="env-name">${displayName}</span>
                    ${env.status === 'available' ? `<span class="edit-name-icon" onclick="promptForNewName('${env.id}', '${env.display_name || ''}')">✏️</span>` : ''}
                </div>
                ${statusLabelHtml}
            </div>
            <div class="env-item-body">
                <div class="env-info">
                    <div class="env-details">
                        ID: ${env.id.substring(0, 8)}<br>
                        Kubernetes: ${env.k8s_version || 'N/A'}<br>
                        Created: ${env.status_updated_at ? formatDate(env.status_updated_at) : 'N/A'}<br>
                        Expires: ${env.expires_at ? formatDate(env.expires_at) : 'N/A'}
                        ${env.error_message ? `<span class="env-error-msg">${env.error_message}</span>` : ''}
                    </div>
                </div>
            </div>
            ${showActionButtons ? `
            <div class="env-item-footer">
                <div class="env-actions">
                    ${buttonHtml}
                </div>
            </div>` : ''}
        </div>`;
    }).join('');
}

function promptForNewName(envId, currentName) {
    const newName = prompt("Enter a new name for the environment (or leave blank to use ID):", currentName);
    if (newName !== null) {
        updateEnvironmentDisplayName(envId, newName.trim());
    }
}

async function updateEnvironmentDisplayName(envId, newDisplayName) {
    try {
        const response = await fetch(`/api/environments/${envId}/displayname`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ display_name: newDisplayName })
        });
        if (response.ok) {
            loadEnvironments();
        } else {
            const error = await response.json();
            alert('Failed to update environment name: ' + (error.error || 'Unknown error'));
        }
    } catch (error) {
        console.error('Failed to update environment name:', error);
        alert('Failed to update environment name: ' + error.message);
    }
}

function createEnvironmentFromMainPanel() {
    const k8sVersionSelect = document.getElementById('k8s-version-main');
    const envNameInput = document.getElementById('env-name-main');
    if (k8sVersionSelect && envNameInput) {
        const sidebarK8sVersionSelect = document.getElementById('k8s-version');
        const sidebarEnvNameInput = document.getElementById('env-name-sidebar');
        if (sidebarK8sVersionSelect) {
            sidebarK8sVersionSelect.value = k8sVersionSelect.value;
        }
        if (sidebarEnvNameInput) {
            sidebarEnvNameInput.value = envNameInput.value;
        }
        createEnvironment();
    } else {
        console.error("k8s-version-main or env-name-main select not found");
    }
}

async function createEnvironment() {
    const k8sVersionSelect = document.getElementById('k8s-version');
    const envNameInput = document.getElementById('env-name-sidebar');
    if (!k8sVersionSelect || !envNameInput) {
        alert('Kubernetes version selector or name input not found.');
        return;
    }
    const k8sVersion = k8sVersionSelect.value;
    if (!k8sVersion) { // ★ バージョンが選択されていない場合は警告
        alert('Please select a Kubernetes version.');
        return;
    }
    const displayName = envNameInput.value.trim();

    try {
        const response = await fetch('/api/environments', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', },
            body: JSON.stringify({ 
                k8s_version: k8sVersion,
                display_name: displayName
            })
        });
        if (response.ok) {
            envNameInput.value = '';
            loadEnvironments();
        } else {
            const error = await response.json();
            alert('Failed to create environment: ' + (error.error || 'Unknown error'));
        }
    } catch (error) {
        console.error('Failed to create environment:', error);
        alert('Failed to create environment: ' + error.message);
    }
}

async function destroyEnvironment(id) {
    if (!confirm('Are you sure you want to destroy this environment? This action cannot be undone.')) {
        return;
    }
    
    if (activeSessions.has(id)) {
        disconnectTerminal(id, currentEnvId === id);
    }

    try {
        const response = await fetch(`/api/environments/${id}`, { method: 'DELETE' });
        if (response.ok) {
            if (currentEnvId === id) { 
                currentEnvId = null;
                mainPanel.innerHTML = '<div style="text-align:center; color: #777; margin-top: 50px; padding:2rem;">Select an environment to connect or create a new one.</div>';
                appLayout.classList.remove('terminal-active');
                appLayout.classList.remove('sidebar-collapsed'); 
            }
            loadEnvironments(); 
        } else {
            const error = await response.json();
            alert('Failed to destroy environment: ' + (error.error || 'Unknown error'));
            loadEnvironments(); 
        }
    } catch (error) {
        console.error('Failed to destroy environment:', error);
        alert('Failed to destroy environment: ' + error.message);
        loadEnvironments(); 
    }
}

async function showTerminalForEnv(id) {
    // Reset browser state when switching to terminal only
    isBrowserVisible = false;
    
    let session = activeSessions.get(id);

    if (!session || !session.term || session.term.isDisposed) {
        await connectEnvironment(id); 
        return; 
    }
    
    if (!session.socket || session.socket.readyState === WebSocket.CLOSED || session.socket.readyState === WebSocket.CLOSING) {
        session.isConnecting = true;
        renderSidebarContent(); 
        try {
            await connectWebSocket(id, session);
            session.isConnecting = false;
        } catch (error) {
            console.error(`Failed to reconnect WebSocket for ${id}:`, error);
            session.isConnecting = false;
            if (session.term && !session.term.isDisposed) {
                const env = environments.find(e => e.id === id);
                const displayName = env ? (env.display_name || env.id.substring(0,8)) : id.substring(0,8);
                session.term.write(`\r\n\x1b[31mFailed to reconnect to environment '${displayName}'. Please try again.\x1b[0m\r\n`);
            }
            renderSidebarContent(); 
            return; 
        }
    }
    
    currentEnvId = id; 
    showTerminalPanelDOM(id, session.term); 
    session.term.focus();
    
    if (session.fitAddon) {
         try { 
             session.fitAddon.fit(); 
             sendTerminalSize(id); 
         } catch(e) { console.error("Error fitting addon on show:", e); }
    }

    if (appLayout) {
        appLayout.classList.remove('no-environments');
        appLayout.classList.add('terminal-active');
    }
    renderSidebarContent(); 
}

async function connectEnvironment(id) {
    let session = activeSessions.get(id);

    if (session && session.isConnecting) {
        return;
    }

    if (session && session.term && !session.term.isDisposed && session.socket && session.socket.readyState === WebSocket.OPEN) {
        await showTerminalForEnv(id); 
        return;
    }
    
    if (session) { 
        if (session.socket && session.socket.readyState !== WebSocket.CLOSED) {
            session.socket.onclose = () => {}; 
            session.socket.close();
        }
        if (session.onDataDisposable) {
            session.onDataDisposable.dispose();
            session.onDataDisposable = null;
        }
    } else {
        const newTerm = new Terminal({
            cursorBlink: true, convertEol: true, disableStdin: false, allowTransparency: false,
            fontFamily: 'Monaco, Menlo, "Ubuntu Mono", Consolas, "Courier New", monospace',
            fontSize: 14, scrollOnUserInput: true, scrollback: 1000, smoothScrollDuration: 0,
            screenReaderMode: false, rendererType: 'dom',
            theme: { 
                background: '#1e1e1e', foreground: '#d4d4d4', cursor: '#aeafad',
                black: '#000000', red: '#cd3131', green: '#0dbc79', yellow: '#e5e510',
                blue: '#2472c8', magenta: '#bc3fbc', cyan: '#11a8cd', white: '#e5e5e5',
                brightBlack: '#666666', brightRed: '#f14c4c', brightGreen: '#23d18b',
                brightYellow: '#f5f543', brightBlue: '#3b8eea', brightMagenta: '#d670d6',
                brightCyan: '#29b8db', brightWhite: '#e5e5e5'
            }
        });
        const newFitAddon = new FitAddon.FitAddon();
        newTerm.loadAddon(newFitAddon);
        session = { term: newTerm, socket: null, fitAddon: newFitAddon, isConnecting: false, onDataDisposable: null };
        activeSessions.set(id, session);
    }
    
    currentEnvId = id; 
    session.isConnecting = true; 
    
    if (appLayout) {
        appLayout.classList.remove('no-environments');
        appLayout.classList.add('terminal-active');
    }
    
    showTerminalPanelDOM(id, session.term); 
    renderSidebarContent(); 

    try {
        await connectWebSocket(id, session); 
        session.isConnecting = false;
        renderSidebarContent(); 
    } catch (error) {
        console.error(`Failed to connect WebSocket for ${id}:`, error);
        session.isConnecting = false;
        if (session.term && !session.term.isDisposed) {
             const env = environments.find(e => e.id === id);
             const displayName = env ? (env.display_name || env.id.substring(0,8)) : id.substring(0,8);
             session.term.write(`\r\n\x1b[31mFailed to connect to environment '${displayName}'. Please try again.\x1b[0m\r\n`);
        }
        if (session.socket) {
            session.socket.onclose = () => {};
            session.socket.close();
            session.socket = null;
        }
        renderSidebarContent(); 
    }
}

function showTerminalPanelDOM(envId, termInstance) {
    if (!mainPanel) return;
    mainPanel.innerHTML = ''; 

    const terminalPanel = document.createElement('div');
    terminalPanel.id = 'terminal-panel'; 
    
    const env = environments.find(e => e.id === envId);
    const displayName = env ? (env.display_name || env.id.substring(0,8)) : envId.substring(0,8);

    terminalPanel.innerHTML = `
        <div class="terminal-header">
            <span class="terminal-title">Environment Terminal (Env: ${displayName})</span>
            <button class="close-terminal" onclick="disconnectCurrentTerminal()">×</button>
        </div>
        <div id="terminal-container-${envId}" class="terminal-container-instance" style="height: calc(100% - 40px);"></div>
    `;
    mainPanel.appendChild(terminalPanel);

    const terminalContainer = document.getElementById(`terminal-container-${envId}`);
    if (terminalContainer && termInstance) {
        if (!termInstance.element || termInstance.element.parentElement !== terminalContainer) { 
            termInstance.open(terminalContainer);
        }
        setTimeout(() => { 
            const session = activeSessions.get(envId); 
            if (session && session.fitAddon && termInstance.element && !termInstance.isDisposed) {
                try {
                    session.fitAddon.fit();
                    sendTerminalSize(envId); 
                } catch (e) {
                    console.error("Error fitting terminal in showTerminalPanelDOM:", e);
                }
            }
            termInstance.focus();
        }, 50); 
    } else {
        console.error(`Terminal container for ${envId} not found or termInstance is invalid for DOM operation!`);
    }
}

function connectWebSocket(environmentId, sessionData) {
    return new Promise((resolve, reject) => {
        if (sessionData.socket && sessionData.socket.readyState !== WebSocket.CLOSED) {
            sessionData.socket.onopen = null;
            sessionData.socket.onmessage = null;
            sessionData.socket.onerror = null;
            sessionData.socket.onclose = null;
            if (sessionData.socket.readyState === WebSocket.OPEN || sessionData.socket.readyState === WebSocket.CONNECTING) {
                sessionData.socket.close(1000, "Reconnecting");
            }
            sessionData.socket = null;
        }
        
        if (sessionData.onDataDisposable) {
            sessionData.onDataDisposable.dispose();
            sessionData.onDataDisposable = null;
        }

        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/api/environments/${environmentId}/connect`;
        const newSocket = new WebSocket(wsUrl);
        newSocket.binaryType = 'arraybuffer';
        sessionData.socket = newSocket; 

        newSocket.onopen = function() {
            if (sessionData.term && !sessionData.term.isDisposed) {
                const env = environments.find(e => e.id === environmentId);
                const displayName = env ? (env.display_name || env.id.substring(0,8)) : environmentId.substring(0,8);
                 sessionData.term.write(`\r\n\x1b[32mConnection to '${displayName}' established.\x1b[0m\r\n`);
            }
            
            const initMessage = {
                cols: sessionData.term && !sessionData.term.isDisposed ? sessionData.term.cols : 80,
                rows: sessionData.term && !sessionData.term.isDisposed ? sessionData.term.rows : 24
            };
            newSocket.send(JSON.stringify(initMessage));
            
            if (sessionData.term && !sessionData.term.isDisposed) {
                sessionData.onDataDisposable = sessionData.term.onData(function(data) { 
                    if (newSocket && newSocket.readyState === WebSocket.OPEN) {
                        newSocket.send(data);
                    }
                });
            }
            setTimeout(() => { 
                sendTerminalSize(environmentId);
                if (sessionData.term && !sessionData.term.isDisposed) sessionData.term.focus();
                handleTerminalResize(environmentId); 
            }, 100);
            resolve(newSocket); 
        };

        newSocket.onmessage = function(event) {
            if (sessionData.term && !sessionData.term.isDisposed) {
                if (event.data instanceof ArrayBuffer) {
                    sessionData.term.write(new Uint8Array(event.data));
                } else {
                    sessionData.term.write(event.data);
                }
            }
        };

        newSocket.onerror = function(errorEvent) {
            console.error(`WebSocket error for ${environmentId}:`, errorEvent);
            if (sessionData.term && !sessionData.term.isDisposed) {
                const env = environments.find(e => e.id === environmentId);
                const displayName = env ? (env.display_name || env.id.substring(0,8)) : environmentId.substring(0,8);
                let errorMessage = `WebSocket connection error for '${displayName}'.`;
                sessionData.term.write(`\r\n\x1b[31m${errorMessage}\x1b[0m\r\n`);
            }
            reject(errorEvent); 
        };

        newSocket.onclose = function(event) {
            console.log(`WebSocket closed for ${environmentId}. Code: ${event.code}, Reason: '${event.reason}', WasClean: ${event.wasClean}`);
            const currentSessionOnClose = activeSessions.get(environmentId); 

            if (currentSessionOnClose && currentSessionOnClose.socket === newSocket) { 
                currentSessionOnClose.socket = null; 
                 if (currentSessionOnClose.onDataDisposable) {
                    currentSessionOnClose.onDataDisposable.dispose();
                    currentSessionOnClose.onDataDisposable = null;
                }
            }
            const env = environments.find(e => e.id === environmentId);
            const displayName = env ? (env.display_name || env.id.substring(0,8)) : environmentId.substring(0,8);

            if (sessionData.term && !sessionData.term.isDisposed) {
                 sessionData.term.write(event.code !== 1000 ? `\r\n\x1b[31m[Connection lost for '${displayName}' - Code: ${event.code}, Reason: ${event.reason || 'N/A'}]\x1b[0m\r\n` : `\r\n\x1b[33m[Connection closed for '${displayName}']\x1b[0m\r\n`);
            }
            renderSidebarContent(); 
        };
    });
}

function sendTerminalSize(envId) {
    const session = activeSessions.get(envId);
    if (session && session.term && !session.term.isDisposed && session.socket && session.socket.readyState === WebSocket.OPEN) {
        try {
            const resizeMessage = JSON.stringify({ resize: true, cols: session.term.cols, rows: session.term.rows });
            session.socket.send(resizeMessage);
        } catch (e) {
            console.error(`Error sending terminal size for ${envId}:`, e);
        }
    }
}

function handleTerminalResize(specificEnvId = null) {
    const envIdToResize = specificEnvId || currentEnvId; 
    if (envIdToResize) {
        const session = activeSessions.get(envIdToResize);
        const terminalContainer = document.getElementById(`terminal-container-${envIdToResize}`);
        if (session && session.term && !session.term.isDisposed && session.fitAddon && terminalContainer) {
            try {
                session.fitAddon.fit();
                sendTerminalSize(envIdToResize); 
            } catch (e) { console.error(`Error resizing terminal for ${envIdToResize}:`, e); }
        }
    }
}
window.addEventListener('resize', () => handleTerminalResize());

function disconnectCurrentTerminal() {
    if (currentEnvId) {
        disconnectTerminal(currentEnvId, true); 
    }
}

function disconnectTerminal(envId, isUIRefreshNeeded = true) {
    const session = activeSessions.get(envId);
    if (session) {
        if (session.socket) {
            session.socket.onopen = null; 
            session.socket.onmessage = null;
            session.socket.onerror = null;
            session.socket.onclose = () => {}; 
            if (session.socket.readyState === WebSocket.OPEN || session.socket.readyState === WebSocket.CONNECTING) {
                session.socket.close(1000, 'Terminal closed by user');
            }
            session.socket = null;
        }
        if (session.onDataDisposable) { 
            session.onDataDisposable.dispose();
            session.onDataDisposable = null;
        }
    }

    if (isUIRefreshNeeded) {
        if (currentEnvId === envId) { 
            currentEnvId = null;
            const terminalPanel = document.getElementById('terminal-panel');
            if (terminalPanel) {
                terminalPanel.remove();
            }
            appLayout.classList.remove('terminal-active');
            renderUIBasedOnState(); 
        } else {
            renderSidebarContent(); 
        }
    }
    const envStillExists = environments.some(e => e.id === envId);
    if (!envStillExists && session) {
        if (session.term && !session.term.isDisposed) {
            session.term.dispose();
        }
        activeSessions.delete(envId);
    }
}

function formatDate(dateString) {
    const date = new Date(dateString);
    if (isNaN(date.getTime())) return 'Invalid date';
    const now = new Date();
    const diff = now - date;
    if (diff < 0) return 'in the future'; 
    if (diff < 60000) return 'just now'; 
    if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`; 
    if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`; 
    return `${Math.floor(diff / 86400000)}d ago`; 
}

document.addEventListener('keydown', function(event) {
    if (event.key === 'Escape' && currentEnvId && (document.getElementById('terminal-panel') || document.getElementById('browser-panel') || document.getElementById('split-view'))) {
        const terminalPanel = document.getElementById('terminal-panel');
        const browserPanel = document.getElementById('browser-panel');
        const splitView = document.getElementById('split-view');
        
        if(terminalPanel || browserPanel || splitView) {
            mainPanel.innerHTML = '<div style="text-align:center; color: #777; margin-top: 50px; padding:2rem;">Select an environment to connect or create a new one.</div>';
        }
        appLayout.classList.remove('terminal-active');
        
        const previouslyActiveId = currentEnvId;
        currentEnvId = null;
        isBrowserVisible = false; // Reset browser state
        
        renderSidebarContent(); 
    }
});

// Browser functionality variables
let browserServices = [];
let currentBrowserUrl = '';
let isBrowserVisible = false;
let splitRatio = 0.5; // 50/50 split by default

// Load services for browser functionality
async function loadBrowserServices(envId) {
    console.log('loadBrowserServices called for envId:', envId);
    try {
        const response = await fetch(`/api/environments/${envId}/services`);
        console.log('Services API response status:', response.status);
        if (!response.ok) {
            console.error('Failed to load services:', response.status);
            return [];
        }
        const data = await response.json();
        console.log('Services data received:', data);
        return data.services || [];
    } catch (error) {
        console.error('Error loading services:', error);
        console.error('Error stack:', error.stack);
        return [];
    }
}

// Show browser with terminal (split view)
async function showBrowserTab(envId) {
    console.log('showBrowserTab called with envId:', envId);
    
    // Open popup immediately to avoid popup blocker
    const popupWidth = 1200;
    const popupHeight = 800;
    const left = (screen.width - popupWidth) / 2;
    const top = (screen.height - popupHeight) / 2;
    
    console.log('Opening popup window immediately...');
    
    let popup;
    try {
        popup = window.open(
            'about:blank', 
            `browser_${envId.replace(/-/g, '_')}`,
            `width=${popupWidth},height=${popupHeight},left=${left},top=${top},scrollbars=yes,resizable=yes,toolbar=no,menubar=no,status=no`
        );
        
        if (!popup || popup.closed || typeof popup.closed == 'undefined') {
            console.error('Popup blocked!');
            alert('Popup blocked! Please allow popups for this site and try again.');
            return;
        }
        
        console.log('Popup opened successfully!');
        
        // Show loading in popup while services load
        popup.document.write('<html><head><title>Loading...</title></head><body style="font-family: Arial, sans-serif; text-align: center; padding: 50px;"><h2>Loading browser...</h2><p>Please wait while we load the services.</p><div id="loading-status">Initializing...</div></body></html>');
        
        // Load services
        let services;
        try {
            services = await loadBrowserServices(envId);
            console.log('Services loaded successfully:', services);
        } catch (serviceError) {
            console.error('Error loading services:', serviceError);
            services = []; // Use empty array as fallback
        }
        
        // Create browser interface in popup
        createBrowserPopup(popup, envId, services);
        
    } catch (error) {
        console.error('Error in showBrowserTab:', error);
        alert('Error opening browser: ' + error.message);
    }
}

// Create browser interface in popup window
function createBrowserPopup(popup, envId, services) {
    console.log('createBrowserPopup called with:', { popup, envId, services });
    const env = environments.find(e => e.id === envId);
    const displayName = env ? (env.display_name || env.id.substring(0,8)) : envId.substring(0,8);
    
    // Create optimized popup HTML content for maximum browser viewing area
    const popupHTML = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Browser - ${displayName}</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; height: 100vh; display: flex; flex-direction: column; }
        
        .header { background: #f8f9fa; border-bottom: 1px solid #dee2e6; padding: 0.75rem 1rem; flex-shrink: 0; }
        .title { font-size: 1.1rem; font-weight: 600; margin-bottom: 0.5rem; }
        
        .controls { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; align-items: end; }
        
        .control-group { display: flex; flex-direction: column; }
        .control-label { font-size: 0.85rem; font-weight: 500; margin-bottom: 0.25rem; color: #495057; }
        
        .input-group { display: flex; gap: 0.5rem; }
        .input-group input, .input-group select { flex: 1; padding: 0.4rem 0.6rem; border: 1px solid #ced4da; border-radius: 4px; font-size: 0.9rem; }
        .btn { background: #007bff; color: white; border: none; padding: 0.4rem 0.8rem; border-radius: 4px; cursor: pointer; font-size: 0.9rem; white-space: nowrap; }
        .btn:hover { background: #0056b3; }
        .btn-secondary { background: #6c757d; }
        .btn-secondary:hover { background: #545b62; }
        
        .content { flex: 1; display: flex; flex-direction: column; overflow: hidden; }
        .browser-frame { flex: 1; }
        .browser-iframe { width: 100%; height: 100%; border: none; }
        
        .no-services { text-align: center; padding: 1rem; color: #6c757d; font-size: 0.9rem; }
    </style>
</head>
<body>
    <div class="header">
        <div class="title">🌐 Browser - ${displayName}</div>
        <div class="controls">
            <div class="control-group">
                <label class="control-label">Kubernetes Services</label>
                <div class="input-group">
                    <select id="k8s-service-select">
                        <option value="">Select a service...</option>
                    </select>
                    <button class="btn" onclick="navigateToK8sService()">Open</button>
                    <button class="btn btn-secondary" onclick="refreshServices()">🔄</button>
                </div>
            </div>
            <div class="control-group">
                <label class="control-label">Custom URL</label>
                <div class="input-group">
                    <input type="text" id="custom-url-input" placeholder="http://localhost:3000" 
                           onkeypress="if(event.key==='Enter') navigateToCustomURL()" />
                    <button class="btn" onclick="navigateToCustomURL()">Go</button>
                </div>
            </div>
        </div>
    </div>
    <div class="content">
        <div class="browser-frame" id="browser-frame">
            <iframe class="browser-iframe" id="browser-iframe" src="about:blank"></iframe>
        </div>
    </div>
    
    <script>
        const envId = '${envId}';
        let currentServices = ${JSON.stringify(services)};
        
        // Navigate to K8s service
        function navigateToK8sService() {
            const select = document.getElementById('k8s-service-select');
            const selectedValue = select.value;
            if (!selectedValue) {
                alert('Please select a service');
                return;
            }
            
            const [serviceName, port] = selectedValue.split(':');
            const iframe = document.getElementById('browser-iframe');
            const url = '/api/environments/' + envId + '/browser/?port=' + port;
            
            // Show loading state
            showLoadingInIframe('Connecting to ' + serviceName + ' on port ' + port + '...');
            
            // Test connectivity first
            testServiceConnectivity(port).then(isConnectable => {
                if (isConnectable) {
                    iframe.src = url;
                } else {
                    showErrorInIframe('Service not available', 
                        'The service "' + serviceName + '" on port ' + port + ' is not responding. ' +
                        'Please verify the service is running or try refreshing the services list.');
                }
            }).catch(error => {
                console.error('Error testing connectivity:', error);
                // Still try to connect in case the test failed for other reasons
                iframe.src = url;
            });
        }
        
        // Navigate to custom URL
        function navigateToCustomURL() {
            const input = document.getElementById('custom-url-input');
            const iframe = document.getElementById('browser-iframe');
            
            let customURL = input.value.trim();
            if (!customURL) {
                alert('Please enter a URL');
                return;
            }
            
            // Parse and normalize the URL
            const normalizedURL = normalizeURL(customURL);
            if (!normalizedURL) {
                alert('Please enter a valid URL (e.g., http://localhost:3000)');
                return;
            }
            
            // Extract port from normalized URL
            const port = extractPortFromURL(normalizedURL);
            const path = extractPathFromURL(normalizedURL);
            
            // Build proxy URL
            const proxyURL = '/api/environments/' + envId + '/browser' + path + '?port=' + port;
            
            iframe.src = proxyURL;
        }
        
        // Normalize URL and add default port if needed
        function normalizeURL(url) {
            try {
                // Add protocol if missing
                if (!url.startsWith('http://') && !url.startsWith('https://')) {
                    url = 'http://' + url;
                }
                
                const urlObj = new URL(url);
                
                // Default to port 80 if no port specified for http
                if (!urlObj.port && urlObj.protocol === 'http:') {
                    urlObj.port = '80';
                }
                
                return urlObj.href;
            } catch (e) {
                return null;
            }
        }
        
        // Extract port from URL
        function extractPortFromURL(url) {
            try {
                const urlObj = new URL(url);
                return urlObj.port || (urlObj.protocol === 'https:' ? '443' : '80');
            } catch (e) {
                return '80';
            }
        }
        
        // Extract path from URL
        function extractPathFromURL(url) {
            try {
                const urlObj = new URL(url);
                return urlObj.pathname + urlObj.search + urlObj.hash;
            } catch (e) {
                return '/';
            }
        }
        
        // Refresh services
        async function refreshServices() {
            try {
                const response = await fetch('/api/environments/' + envId + '/services');
                if (response.ok) {
                    const data = await response.json();
                    currentServices = data.services || [];
                    populateK8sServiceDropdown(currentServices);
                }
            } catch (error) {
                console.error('Error refreshing services:', error);
            }
        }
        
        // Populate K8s service dropdown
        function populateK8sServiceDropdown(services) {
            const select = document.getElementById('k8s-service-select');
            
            // Clear existing options except the first one
            while (select.children.length > 1) {
                select.removeChild(select.lastChild);
            }
            
            if (services && services.length > 0) {
                services.forEach(service => {
                    const option = document.createElement('option');
                    option.value = service.name + ':' + service.port;
                    option.textContent = service.name + ' (' + service.description + ') - Port ' + service.port;
                    select.appendChild(option);
                });
            } else {
                // Show "No services" option
                const option = document.createElement('option');
                option.value = '';
                option.textContent = 'No services found';
                option.disabled = true;
                select.appendChild(option);
            }
        }
        
        // Initialize
        populateK8sServiceDropdown(currentServices);
        
        // Test service connectivity
        async function testServiceConnectivity(port) {
            try {
                const response = await fetch('/api/environments/' + envId + '/browser/?port=' + port, {
                    method: 'HEAD',
                    signal: AbortSignal.timeout(5000) // 5 second timeout
                });
                return response.ok;
            } catch (error) {
                console.log('Connectivity test failed for port', port, ':', error.message);
                return false;
            }
        }
        
        // Show loading state in iframe
        function showLoadingInIframe(message) {
            const iframe = document.getElementById('browser-iframe');
            const loadingHTML = '<html><body style="font-family: Arial, sans-serif; text-align: center; padding: 50px; background: #f8f9fa;"><div style="font-size: 1.2rem; color: #495057; margin-bottom: 1rem;">🔄 ' + message + '</div><div style="color: #6c757d;">Please wait...</div></body></html>';
            iframe.src = 'data:text/html;charset=utf-8,' + encodeURIComponent(loadingHTML);
        }
        
        // Show error state in iframe
        function showErrorInIframe(title, message) {
            const iframe = document.getElementById('browser-iframe');
            const errorHTML = '<html><body style="font-family: Arial, sans-serif; text-align: center; padding: 50px; background: #f8f9fa;"><div style="font-size: 1.5rem; color: #dc3545; margin-bottom: 1rem;">❌ ' + title + '</div><div style="color: #6c757d; margin-bottom: 2rem; line-height: 1.5;">' + message + '</div><button onclick="parent.refreshServices()" style="background: #007bff; color: white; border: none; padding: 0.5rem 1rem; border-radius: 4px; cursor: pointer;">Refresh Services</button></body></html>';
            iframe.src = 'data:text/html;charset=utf-8,' + encodeURIComponent(errorHTML);
        }
        
        // Set focus on custom URL input for immediate use
        document.getElementById('custom-url-input').focus();
    </script>
</body>
</html>`;
    
    console.log('Writing HTML to popup...');
    try {
        // Clear any existing content first
        popup.document.open();
        popup.document.write(popupHTML);
        popup.document.close();
        console.log('Popup HTML written and document closed successfully');
        
    } catch (writeError) {
        console.error('Error writing to popup document:', writeError);
        throw writeError;
    }
}

// Create split view with terminal and browser
function createSplitView(envId, terminal) {
    console.log('createSplitView called with envId:', envId);
    console.log('Terminal object:', terminal);
    console.log('Terminal type:', typeof terminal);
    console.log('Terminal keys:', Object.keys(terminal || {}));
    
    if (!terminal) {
        console.error('No terminal provided to createSplitView');
        return;
    }
    
    if (!terminal.element) {
        console.error('Terminal has no element property');
        console.log('Available terminal properties:', Object.keys(terminal));
        return;
    }
    
    // Get the session for fitAddon access
    const session = activeSessions.get(envId);
    if (!session) {
        console.error('No session found for envId:', envId);
        return;
    }
    
    const env = environments.find(e => e.id === envId);
    const displayName = env ? (env.display_name || env.id.substring(0,8)) : envId.substring(0,8);
    
    console.log('Creating split view for environment:', displayName);
    
    const splitViewHTML = `
        <div id="split-view" class="split-view">
            <div class="split-top" id="split-terminal">
                <div class="terminal-header">
                    <span class="terminal-title">🖥️ Terminal: ${displayName}</span>
                    <button class="close-terminal" onclick="closeSplitView()">✕</button>
                </div>
                <div class="terminal-container-instance" id="terminal-container-split"></div>
            </div>
            <div class="split-resizer" onmousedown="startResize(event)"></div>
            <div class="split-bottom" id="split-browser">
                <div class="browser-toggle">
                    <span class="browser-toggle-title">🌐 Browser</span>
                    <div class="browser-controls">
                        <button class="browser-toggle-btn" onclick="toggleBrowserPanel()">Collapse</button>
                    </div>
                </div>
                ${createBrowserContent(envId)}
            </div>
        </div>
    `;
    
    console.log('Setting split view HTML');
    mainPanel.innerHTML = splitViewHTML;
    
    // Attach terminal to new container
    const terminalContainer = document.getElementById('terminal-container-split');
    if (!terminalContainer) {
        console.error('Could not find terminal container');
        return;
    }
    
    console.log('Attaching terminal to new container');
    
    // Always try to move the terminal element first
    try {
        if (terminal.element) {
            console.log('Moving existing terminal element');
            // Detach from current parent if exists
            if (terminal.element.parentElement) {
                terminal.element.parentElement.removeChild(terminal.element);
            }
            terminalContainer.appendChild(terminal.element);
        } else {
            console.log('Re-opening terminal in new container');
            terminal.open(terminalContainer);
        }
    } catch (error) {
        console.error('Error moving terminal element:', error);
        // Fallback: re-open terminal
        terminal.open(terminalContainer);
    }
    
    // Resize terminal to fit new container immediately and schedule another resize
    if (terminal && !terminal.isDisposed) {
        console.log('Resizing terminal to fit new container');
        try {
            // Use fitAddon from session instead of terminal.fit
            if (session.fitAddon && typeof session.fitAddon.fit === 'function') {
                session.fitAddon.fit();
            } else {
                console.warn('FitAddon not available in session');
            }
            
            // Check if focus method exists on terminal
            if (typeof terminal.focus === 'function') {
                terminal.focus();
            } else {
                console.warn('Terminal focus method not available');
            }
            
            // Schedule another resize to ensure proper sizing
            setTimeout(() => {
                if (terminal && !terminal.isDisposed && session.fitAddon && typeof session.fitAddon.fit === 'function') {
                    session.fitAddon.fit();
                }
            }, 50);
        } catch (error) {
            console.error('Error resizing terminal:', error);
        }
    }
    
    // Apply split ratio
    console.log('Applying split ratio');
    applySplitRatio();
    
    // Final resize after DOM is fully updated
    setTimeout(() => {
        if (session.fitAddon && typeof session.fitAddon.fit === 'function') {
            console.log('Final terminal resize');
            session.fitAddon.fit();
        }
    }, 100);
}

// Convert existing terminal panel to split view (legacy function - can be removed)
function convertTerminalPanelToSplitView(envId, terminal) {
    // Just delegate to createSplitView now
    createSplitView(envId, terminal);
}

// Create browser content HTML
function createBrowserContent(envId) {
    return `
        <div class="browser-content">
            <div class="browser-toolbar">
                <div class="services-section">
                    <h4>Available Services</h4>
                    <div id="services-list" class="services-list">
                        <div class="loading">Loading services...</div>
                    </div>
                    <button class="btn btn-sm btn-primary" onclick="refreshServices('${envId}')">
                        🔄 Refresh
                    </button>
                </div>
                
                <div class="url-section">
                    <h4>Custom URL</h4>
                    <div class="url-input-group">
                        <input type="text" id="browser-url" placeholder="http://localhost:3000" 
                               value="${currentBrowserUrl}" onkeydown="handleUrlKeydown(event, '${envId}')">
                        <button class="btn btn-primary" onclick="navigateToBrowserUrl('${envId}')">
                            Go
                        </button>
                    </div>
                </div>
            </div>
            
            <div class="browser-iframe-container">
                <iframe id="browser-iframe" src="about:blank" frameborder="0"></iframe>
            </div>
        </div>
    `;
}

// Apply split ratio
function applySplitRatio() {
    const splitTop = document.querySelector('.split-top');
    const splitBottom = document.querySelector('.split-bottom');
    
    if (splitTop && splitBottom) {
        splitTop.style.flex = `${splitRatio}`;
        splitBottom.style.flex = `${1 - splitRatio}`;
    }
}

// Start resize operation
function startResize(event) {
    event.preventDefault();
    
    const startY = event.clientY;
    const splitView = document.getElementById('split-view');
    const rect = splitView.getBoundingClientRect();
    const startRatio = splitRatio;
    
    function onMouseMove(e) {
        const deltaY = e.clientY - startY;
        const containerHeight = rect.height;
        const deltaRatio = deltaY / containerHeight;
        
        splitRatio = Math.max(0.2, Math.min(0.8, startRatio + deltaRatio));
        applySplitRatio();
        
        // Resize terminal
        const session = activeSessions.get(currentEnvId);
        if (session && session.term && !session.term.isDisposed && session.fitAddon) {
            setTimeout(() => session.fitAddon.fit(), 10);
        }
    }
    
    function onMouseUp() {
        document.removeEventListener('mousemove', onMouseMove);
        document.removeEventListener('mouseup', onMouseUp);
    }
    
    document.addEventListener('mousemove', onMouseMove);
    document.addEventListener('mouseup', onMouseUp);
}

// Toggle browser panel collapse
function toggleBrowserPanel() {
    const browserPanel = document.getElementById('split-browser');
    const toggleBtn = document.querySelector('.browser-toggle-btn');
    
    if (browserPanel.classList.contains('collapsed')) {
        browserPanel.classList.remove('collapsed');
        toggleBtn.textContent = 'Collapse';
        splitRatio = 0.5; // Reset to 50/50
    } else {
        browserPanel.classList.add('collapsed');
        toggleBtn.textContent = 'Expand';
        splitRatio = 0.9; // Give most space to terminal
    }
    
    applySplitRatio();
    
    // Resize terminal
    const session = activeSessions.get(currentEnvId);
    if (session && session.term && !session.term.isDisposed && session.fitAddon) {
        setTimeout(() => session.fitAddon.fit(), 100);
    }
}

// Close split view
function closeSplitView() {
    isBrowserVisible = false;
    mainPanel.innerHTML = '<div style="text-align:center; color: #777; margin-top: 50px; padding:2rem;">Select an environment to connect or create a new one.</div>';
    appLayout.classList.remove('terminal-active');
    
    currentEnvId = null;
    renderSidebarContent();
}

// Create browser panel HTML (legacy - keep for compatibility)
function createBrowserPanel(envId) {
    return `
        <div id="browser-panel" class="browser-panel">
            <div class="browser-header">
                <div class="browser-tabs">
                    <button class="browser-tab ${currentBrowserTab === 'terminal' ? 'active' : ''}" 
                            onclick="switchToBrowserTab('terminal', '${envId}')">Terminal</button>
                    <button class="browser-tab ${currentBrowserTab === 'browser' ? 'active' : ''}" 
                            onclick="switchToBrowserTab('browser', '${envId}')">Browser</button>
                </div>
                <div class="browser-controls">
                    <button class="btn btn-sm btn-secondary" onclick="closeBrowserPanel()">✕ Close</button>
                </div>
            </div>
            
            <div class="browser-content">
                <div class="browser-toolbar">
                    <div class="services-section">
                        <h4>Available Services</h4>
                        <div id="services-list" class="services-list">
                            <div class="loading">Loading services...</div>
                        </div>
                        <button class="btn btn-sm btn-primary" onclick="refreshServices('${envId}')">
                            🔄 Refresh Services
                        </button>
                    </div>
                    
                    <div class="url-section">
                        <h4>Custom URL</h4>
                        <div class="url-input-group">
                            <input type="text" id="browser-url" placeholder="http://localhost:3000" 
                                   value="${currentBrowserUrl}" onkeydown="handleUrlKeydown(event, '${envId}')">
                            <button class="btn btn-primary" onclick="navigateToBrowserUrl('${envId}')">
                                Go
                            </button>
                        </div>
                    </div>
                </div>
                
                <div class="browser-iframe-container">
                    <iframe id="browser-iframe" src="about:blank" frameborder="0"></iframe>
                </div>
            </div>
        </div>
    `;
}

// Toggle browser visibility in split view
async function toggleBrowserInSplitView(envId) {
    if (isBrowserVisible) {
        // Hide browser, show only terminal
        const session = activeSessions.get(envId);
        if (session && session.term && !session.term.isDisposed) {
            await showTerminalForEnv(envId);
        }
        isBrowserVisible = false;
    } else {
        // Show browser with terminal
        await showBrowserTab(envId);
    }
}

// Populate services list
function populateServicesList(services) {
    const servicesList = document.getElementById('services-list');
    
    if (!servicesList) {
        console.log('Services list element not found, will populate later');
        return;
    }
    
    let servicesHTML = '';
    
    // Add discovered services
    if (services && services.length > 0) {
        servicesHTML += services.map(service => `
            <div class="service-item">
                <div class="service-info">
                    <div class="service-name">${service.name}</div>
                    <div class="service-description">${service.description}</div>
                    <div class="service-port">Port: ${service.port} (${service.protocol})</div>
                </div>
                <button class="btn btn-sm btn-primary" 
                        onclick="navigateToService('${currentEnvId}', ${service.port})">
                    Open
                </button>
            </div>
        `).join('');
    }
    
    // Add common default ports if no services were found or as additional options
    const commonPorts = [
        { port: 80, name: 'HTTP Server', description: 'Standard HTTP port' },
        { port: 3000, name: 'Node.js App', description: 'Common development port' },
        { port: 8000, name: 'Python Server', description: 'Python SimpleHTTPServer' },
        { port: 8080, name: 'Alt HTTP', description: 'Alternative HTTP port' },
        { port: 9000, name: 'Dev Server', description: 'Development server' }
    ];
    
    // Filter out ports that are already in discovered services
    const discoveredPorts = services ? services.map(s => s.port) : [];
    const additionalPorts = commonPorts.filter(cp => !discoveredPorts.includes(cp.port));
    
    if (additionalPorts.length > 0) {
        if (servicesHTML) {
            servicesHTML += '<div style="margin: 0.5rem 0; border-top: 1px solid #dee2e6; padding-top: 0.5rem;"><small style="color: #6c757d;">Common Ports:</small></div>';
        }
        servicesHTML += additionalPorts.map(cp => `
            <div class="service-item">
                <div class="service-info">
                    <div class="service-name">${cp.name}</div>
                    <div class="service-description">${cp.description}</div>
                    <div class="service-port">Port: ${cp.port} (tcp)</div>
                </div>
                <button class="btn btn-sm btn-secondary" 
                        onclick="navigateToService('${currentEnvId}', ${cp.port})">
                    Try
                </button>
            </div>
        `).join('');
    }
    
    servicesList.innerHTML = servicesHTML || '<div class="no-services">No services found</div>';
}

// Navigate to a specific service
function navigateToService(envId, port) {
    const url = `/api/environments/${envId}/browser/?port=${port}`;
    navigateToUrl(url);
}

// Navigate to custom URL
function navigateToBrowserUrl(envId) {
    const urlInput = document.getElementById('browser-url');
    const url = urlInput.value.trim();
    
    if (!url) {
        alert('Please enter a URL');
        return;
    }
    
    currentBrowserUrl = url;
    
    // Extract port from URL if it's a localhost URL
    let proxyUrl = url;
    if (url.includes('localhost')) {
        // Handle localhost with explicit port
        const explicitPortMatch = url.match(/localhost:(\d+)(.*)$/);
        if (explicitPortMatch) {
            const port = explicitPortMatch[1];
            const path = explicitPortMatch[2] || '/';
            proxyUrl = `/api/environments/${envId}/browser${path}?port=${port}`;
        } else {
            // Handle localhost without port (default to 80)
            const noPortMatch = url.match(/https?:\/\/localhost(\/.*)?$/);
            if (noPortMatch) {
                const path = noPortMatch[1] || '/';
                proxyUrl = `/api/environments/${envId}/browser${path}?port=80`;
            }
        }
    }
    
    navigateToUrl(proxyUrl);
}

// Navigate to URL in iframe
function navigateToUrl(url) {
    const iframe = document.getElementById('browser-iframe');
    iframe.src = url;
}

// Handle URL input keydown
function handleUrlKeydown(event, envId) {
    if (event.key === 'Enter') {
        navigateToBrowserUrl(envId);
    }
}

// Refresh services list
async function refreshServices(envId) {
    const servicesList = document.getElementById('services-list');
    servicesList.innerHTML = '<div class="loading">Loading services...</div>';
    
    browserServices = await loadBrowserServices(envId);
    populateServicesList(browserServices);
}

// サイドバーの手動トグル機能
function toggleSidebar() {
    console.log('🔄 Toggle sidebar');
    appLayout.classList.toggle('sidebar-collapsed');
    const isCollapsed = appLayout.classList.contains('sidebar-collapsed');
    console.log('📏 Sidebar is now:', isCollapsed ? 'collapsed' : 'expanded');
}

// サイドバーがクリックされた時の処理
function handleSidebarClick(event) {
    const isCollapsed = appLayout.classList.contains('sidebar-collapsed');
    console.log('👆 Sidebar clicked | Collapsed:', isCollapsed);
    
    // 最小化状態でのみクリックで展開
    if (isCollapsed) {
        // ボタンをクリックした場合は何もしない（ボタンの処理に任せる）
        if (event.target.closest('.sidebar-toggle')) {
            console.log('🚫 Toggle button clicked, ignoring');
            return;
        }
        // サイドバー内の他の要素（フォームなど）をクリックした場合は何もしない
        if (event.target.closest('.actions, .environments, input, button, select')) {
            console.log('🚫 Interactive element clicked, ignoring');
            return;
        }
        // サイドバーの空いている部分をクリックした場合のみ展開
        console.log('✅ Expanding sidebar via click');
        toggleSidebar();
    }
}

// Close browser panel
function closeBrowserPanel() {
    mainPanel.innerHTML = '<div style="text-align:center; color: #777; margin-top: 50px; padding:2rem;">Select an environment to connect or create a new one.</div>';
    appLayout.classList.remove('terminal-active');
    
    currentEnvId = null;
    currentBrowserTab = 'terminal';
    
    renderSidebarContent();
}
