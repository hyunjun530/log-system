'use strict';

/* ── Constants ──────────────────────────────────────────── */
const SS_KEY = 'log_api_key';
const PAGE_SIZE = 50;
const APP_BASE_PATH = getAppBasePath();

/* ── State ──────────────────────────────────────────────── */
let currentPage = 1;
let selectedLevels = new Set();
let selectedRow = -1; // index in current items
let currentItems = [];
let hasMore = false;
let debounceTimer = null;
let apiKeyTimer = null;
let activePreset = null;

/* ── Init ───────────────────────────────────────────────── */
window.addEventListener('DOMContentLoaded', () => {
  const saved = sessionStorage.getItem(SS_KEY);
  if (saved) document.getElementById('apiKey').value = saved;

  document.getElementById('apiKey').addEventListener('input', e => {
    const key = e.target.value.trim();
    sessionStorage.setItem(SS_KEY, key);
    clearTimeout(apiKeyTimer);
    if (!key) {
      toggleLayout(false);
      return;
    }
    apiKeyTimer = setTimeout(search, 350);
  });

  document.getElementById('apiKey').addEventListener('keydown', e => {
    if (e.key === 'Enter') {
      clearTimeout(apiKeyTimer);
      search();
    }
  });

  document.getElementById('keyword').addEventListener('input', () => {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => { currentPage = 1; fetchLogs(); }, 300);
  });

  document.getElementById('keyword').addEventListener('keydown', e => {
    if (e.key === 'Enter') { clearTimeout(debounceTimer); search(); }
  });

  // Close time popup when clicking outside
  document.addEventListener('click', e => {
    const popup = document.getElementById('timePopup');
    const trigger = document.getElementById('timeTrigger');
    if (popup && trigger && !popup.contains(e.target) && !trigger.contains(e.target)) {
      popup.classList.remove('show');
    }
  });

  restoreFromURL();
  setupKeyboard();
  search();
});

/* ── URL sync ───────────────────────────────────────────── */
function restoreFromURL() {
  const p = new URLSearchParams(location.search);
  if (p.get('level')) {
    p.get('level').split(',').forEach(l => {
      selectedLevels.add(l);
      const chip = document.querySelector(`.chip[data-level="${l}"]`);
      if (chip) chip.classList.add(`active-${l}`);
    });
  }
  if (p.get('q')) document.getElementById('keyword').value = p.get('q');
  if (p.get('from')) setDatetimeInput('fromDt', p.get('from'));
  if (p.get('to'))   setDatetimeInput('toDt',   p.get('to'));
  if (p.get('page')) currentPage = parseInt(p.get('page')) || 1;
  updateTimeTriggerText();
}

function pushURL() {
  const p = new URLSearchParams();
  if (selectedLevels.size) p.set('level', [...selectedLevels].join(','));
  const q = document.getElementById('keyword').value.trim();
  if (q) p.set('q', q);
  const from = getFromISO(); if (from) p.set('from', from);
  const to   = getToISO();   if (to)   p.set('to', to);
  if (currentPage > 1) p.set('page', currentPage);
  const qs = p.toString();
  history.replaceState(null, '', qs ? '?' + qs : location.pathname);
}

/* ── Helpers ─────────────────────────────────────────────── */
function apiKey() { return document.getElementById('apiKey').value.trim(); }

function getAppBasePath() {
  const path = window.location.pathname;
  if (!path || path === '/') return '';

  const staticIdx = path.indexOf('/static/');
  if (staticIdx >= 0) return path.slice(0, staticIdx);

  return path.replace(/\/$/, '');
}

function appPath(path) {
  return APP_BASE_PATH + path;
}

function toggleLayout(authenticated) {
  const authScreen = document.getElementById('authScreen');
  const mainLayout = document.getElementById('mainLayout');
  if (authenticated) {
    authScreen.style.display = 'none';
    mainLayout.style.display = 'flex';
  } else {
    authScreen.style.display = 'flex';
    mainLayout.style.display = 'none';
  }
}

function getFromISO() {
  const v = document.getElementById('fromDt').value;
  return v ? new Date(v).toISOString() : '';
}
function getToISO() {
  const v = document.getElementById('toDt').value;
  return v ? new Date(v).toISOString() : '';
}

function setDatetimeInput(id, isoStr) {
  try {
    const d = new Date(isoStr);
    const local = new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
    document.getElementById(id).value = local;
  } catch (_) {}
}

/* ── Chip ────────────────────────────────────────────────── */
function toggleChip(btn) {
  const lvl = btn.dataset.level;
  if (selectedLevels.has(lvl)) {
    selectedLevels.delete(lvl);
    btn.classList.remove(`active-${lvl}`);
  } else {
    selectedLevels.add(lvl);
    btn.classList.add(`active-${lvl}`);
  }
  currentPage = 1;
  fetchLogs();
}

/* ── Time Picker logic ───────────────────────────────────── */
function toggleTimePopup(e) {
  e.stopPropagation();
  document.getElementById('timePopup').classList.toggle('show');
}

function onTimeChange() {
  clearPreset();
}

function applyTimePopup() {
  document.getElementById('timePopup').classList.remove('show');
  updateTimeTriggerText();
  currentPage = 1;
  fetchLogs();
}

function resetTimePopup() {
  document.getElementById('fromDt').value = '';
  document.getElementById('toDt').value = '';
  clearPreset();
  updateTimeTriggerText();
  document.getElementById('timePopup').classList.remove('show');
  currentPage = 1;
  fetchLogs();
}

function updateTimeTriggerText() {
  const from = document.getElementById('fromDt').value;
  const to = document.getElementById('toDt').value;
  const textEl = document.getElementById('timeRangeText');
  
  if (activePreset) {
    const btn = document.querySelector(`.preset-btn[data-min="${activePreset}"]`);
    textEl.textContent = btn ? btn.textContent : '최근 시간';
    return;
  }
  
  if (!from && !to) {
    textEl.textContent = '전체 시간';
  } else if (from && to) {
    textEl.textContent = `${from.slice(5, 16)} ~ ${to.slice(5, 16)}`;
  } else if (from) {
    textEl.textContent = `${from.slice(5, 16)} 이후`;
  } else {
    textEl.textContent = `${to.slice(5, 16)} 이전`;
  }
}

/* ── Presets ─────────────────────────────────────────────── */
function setPreset(btn) {
  const mins = parseInt(btn.dataset.min);
  document.querySelectorAll('.preset-grid .preset-btn').forEach(b => b.classList.remove('active'));
  
  if (activePreset === mins) {
    activePreset = null;
    document.getElementById('fromDt').value = '';
    document.getElementById('toDt').value   = '';
  } else {
    activePreset = mins;
    btn.classList.add('active');
    const now = new Date();
    const from = new Date(now.getTime() - mins * 60000);
    setDatetimeInput('fromDt', from.toISOString());
    document.getElementById('toDt').value = '';
  }
  
  updateTimeTriggerText();
  document.getElementById('timePopup').classList.remove('show');
  currentPage = 1;
  fetchLogs();
}

function clearPreset() {
  activePreset = null;
  document.querySelectorAll('.preset-grid .preset-btn').forEach(b => b.classList.remove('active'));
}

/* ── Filters ─────────────────────────────────────────────── */
function resetFilters() {
  selectedLevels.clear();
  document.querySelectorAll('.chip').forEach(c => c.className = 'chip');
  document.getElementById('keyword').value = '';
  document.getElementById('fromDt').value  = '';
  document.getElementById('toDt').value    = '';
  clearPreset();
  updateTimeTriggerText();
  currentPage = 1;
  closeDrawer();
  fetchLogs();
}

/* ── Search / Page ───────────────────────────────────────── */
function search() { currentPage = 1; fetchLogs(); }
function goPage(delta) { currentPage = Math.max(1, currentPage + delta); fetchLogs(); }

/* ── Fetch ───────────────────────────────────────────────── */
async function fetchLogs() {
  const key   = apiKey();
  const level = [...selectedLevels].join(',');
  const q     = document.getElementById('keyword').value.trim();
  const from  = getFromISO();
  const to    = getToISO();
  const btn   = document.getElementById('searchBtn');

  if (!key) {
    toggleLayout(false);
    return;
  }

  btn.disabled = true;
  setStatus('loading', '조회 중…');
  hideError();

  const params = new URLSearchParams({ page: currentPage, size: PAGE_SIZE });
  if (level) params.set('level', level);
  if (q)     params.set('q', q);
  if (from)  params.set('from', from);
  if (to)    params.set('to', to);

  showSkeleton();

  try {
    const res = await fetch(appPath('/api/logs') + '?' + params, { headers: { 'X-API-Key': key } });

    if (res.status === 401) {
      showError('401 인증 실패 — API Key를 확인하세요.');
      toggleLayout(false); // Hide UI on auth failure
      renderEmpty(q, level);
      setStatus('error', '인증 오류');
      return;
    }
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      showError(`오류: ${body.error || res.statusText}`);
      renderEmpty(q, level);
      setStatus('error', '오류');
      return;
    }

    const data = await res.json();
    toggleLayout(true); // Show UI on success
    sessionStorage.setItem(SS_KEY, key);
    renderTable(data, q);
    setStatus('', '');
    pushURL();
  } catch (err) {
    showError('네트워크 오류: ' + err.message);
    renderEmpty(q, level);
    setStatus('error', '오류');
  } finally {
    btn.disabled = false;
  }
}

/* ── Render ──────────────────────────────────────────────── */
function showSkeleton() {
  document.getElementById('mainTable').style.display = '';
  document.getElementById('emptyState').style.display = 'none';
  const tbody = document.getElementById('tbody');
  tbody.innerHTML = Array(7).fill(0).map((_, i) => `
    <tr class="skeleton-row">
      <td><div class="skel" style="width:${70+i%3*10}%"></div></td>
      <td><div class="skel" style="width:50px"></div></td>
      <td><div class="skel" style="width:${50+i%4*12}%"></div></td>
    </tr>`).join('');
}

function renderTable(data, q) {
  currentItems = data.items || [];
  const total  = data.total || 0;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  hasMore = currentPage < totalPages;

  const tbody  = document.getElementById('tbody');
  const empty  = document.getElementById('emptyState');
  const table  = document.getElementById('mainTable');

  if (currentItems.length === 0) {
    table.style.display = 'none';
    empty.style.display  = 'flex';
    document.getElementById('emptyDetail').textContent =
      buildActiveFiltersText() || '조회할 로그가 없습니다.';
  } else {
    empty.style.display  = 'none';
    table.style.display  = '';
    tbody.innerHTML = currentItems.map((e, i) => `
      <tr data-idx="${i}" onclick="selectRow(${i})">
        <td class="col-ts" title="${new Date(e.timestamp).toISOString()}">${formatTs(e.timestamp)}</td>
        <td class="col-lvl col-lvl-${escHtml(e.level)}">${escHtml(e.level)}</td>
        <td class="col-msg">${renderMessage(e.message, q)}</td>
      </tr>`).join('');
  }

  document.getElementById('pageInfo').textContent  = `${currentPage} 페이지`;
  document.getElementById('totalInfo').textContent = total
    ? `${total.toLocaleString()}건${hasMore ? '+' : ''}`
    : '';
  document.getElementById('prevBtn').disabled = currentPage <= 1;
  document.getElementById('nextBtn').disabled = !hasMore;

  const lvlText = selectedLevels.size ? [...selectedLevels].join(', ') : '전체';
  const qText   = document.getElementById('keyword').value.trim();
  document.getElementById('resultSummary').innerHTML =
    `<strong>${total.toLocaleString()}</strong>건 · Level: ${escHtml(lvlText)}` +
    (qText ? ` · "<strong>${escHtml(qText)}</strong>"` : '');

  selectedRow = -1;
}

function renderEmpty(q, level) {
  currentItems = [];
  document.getElementById('mainTable').style.display = 'none';
  const empty = document.getElementById('emptyState');
  empty.style.display = 'flex';
  document.getElementById('emptyDetail').textContent = buildActiveFiltersText() || '';
  document.getElementById('tbody').innerHTML = '';
  document.getElementById('pageInfo').textContent  = '-';
  document.getElementById('totalInfo').textContent = '';
  document.getElementById('prevBtn').disabled = true;
  document.getElementById('nextBtn').disabled = true;
  document.getElementById('resultSummary').textContent = '';
}

function buildActiveFiltersText() {
  const parts = [];
  if (selectedLevels.size) parts.push(`Level: ${[...selectedLevels].join(', ')}`);
  const q = document.getElementById('keyword').value.trim();
  if (q) parts.push(`키워드: "${q}"`);
  const from = document.getElementById('fromDt').value;
  const to   = document.getElementById('toDt').value;
  if (from || to) parts.push(`시간 범위 설정됨`);
  return parts.length ? '현재 필터: ' + parts.join(' / ') : '';
}

/* ── Message rendering ───────────────────────────────────── */
function renderMessage(msg, q) {
  if (msg === null || msg === undefined) return '';
  if (typeof msg !== 'object') return highlightKeyword(escHtml(String(msg)), q);
  return Object.entries(msg)
    .map(([k, v]) => {
      const val = typeof v === 'object' ? JSON.stringify(v) : String(v);
      return `<span class="kv"><span class="kv-key">${escHtml(k)}</span><span class="kv-val">${highlightKeyword(escHtml(val), q)}</span></span>`;
    })
    .join('');
}

function highlightKeyword(escaped, q) {
  if (!q) return escaped;
  const lower = escaped.toLowerCase();
  const qLower = q.toLowerCase();
  let out = '', i = 0;
  while (i < escaped.length) {
    const idx = lower.indexOf(qLower, i);
    if (idx === -1) { out += escaped.slice(i); break; }
    out += escaped.slice(i, idx) + '<mark>' + escaped.slice(idx, idx + q.length) + '</mark>';
    i = idx + q.length;
  }
  return out;
}

/* ── Timestamp formatting ────────────────────────────────── */
function formatTs(ms) {
  try {
    const d = new Date(ms);
    const pad = n => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
  } catch (_) {
    return escHtml(ms);
  }
}

function relativeTime(ms) {
  try {
    const diff = Date.now() - ms;
    if (diff < 0)       return '방금 전';
    if (diff < 60000)   return `${Math.floor(diff/1000)}초 전`;
    if (diff < 3600000) return `${Math.floor(diff/60000)}분 전`;
    if (diff < 86400000)return `${Math.floor(diff/3600000)}시간 전`;
    return `${Math.floor(diff/86400000)}일 전`;
  } catch (_) { return ''; }
}

function localTs(ms, includeMs = false) {
  try {
    const d = new Date(ms);
    const pad = n => String(n).padStart(2, '0');
    const pms = n => String(n).padStart(3, '0');
    
    let base = `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
    if (includeMs) {
      base += `.${pms(d.getMilliseconds())}`;
    }
    return base;
  } catch (_) { return ''; }
}

/* ── Row selection & Drawer ──────────────────────────────── */
function selectRow(idx) {
  if (selectedRow === idx) { closeDrawer(); return; }
  selectedRow = idx;
  document.querySelectorAll('#tbody tr').forEach((tr, i) => {
    tr.classList.toggle('selected', i === idx);
  });
  openDrawer(currentItems[idx]);
}

function openDrawer(entry) {
  const drawer = document.getElementById('drawer');
  drawer.classList.add('open');
  renderDrawer(entry);
}

function closeDrawer() {
  document.getElementById('drawer').classList.remove('open');
  document.querySelectorAll('#tbody tr').forEach(tr => tr.classList.remove('selected'));
  selectedRow = -1;
}

function renderDrawer(entry) {
  const body = document.getElementById('drawerBody');

  const ts       = entry.timestamp;
  const local    = localTs(ts, true);
  const relative = relativeTime(ts);
  const iso      = new Date(ts).toISOString();

  const lvlClass = `lvl-badge lvl-badge-${escHtml(entry.level)}`;

  body.innerHTML = `
    <div class="drawer-field">
      <label>Timestamp</label>
      <div class="ts-stack">
        <div class="ts-row">
          <span class="ts-label">Local</span>
          <span class="ts-val">${escHtml(local)}</span>
          ${copyMiniBtn(local, '로컬 시간 복사')}
        </div>
        <div class="ts-row">
          <span class="ts-label">Unix</span>
          <span class="ts-val">${escHtml(ts)}</span>
          ${copyMiniBtn(ts, 'Unix Timestamp 복사')}
        </div>
        <div class="ts-row">
          <span class="ts-label">ISO</span>
          <span class="ts-val" style="font-size:10px; color:var(--muted)">${escHtml(iso)}</span>
        </div>
        <div class="ts-row">
          <span class="ts-label">Ago</span>
          <span class="ts-val relative">${escHtml(relative)}</span>
        </div>
      </div>
    </div>

    <div class="drawer-field">
      <label>Level</label>
      <span class="${lvlClass}">${escHtml(entry.level)}</span>
    </div>

    <div class="drawer-field">
      <label>Message</label>
      <pre class="json-pre">${jsonHighlight(entry.message)}</pre>
    </div>
  `;

  // Store for copyAll
  const drawer = document.getElementById('drawer');
  drawer.dataset.entry = JSON.stringify(entry);
}

function copyMiniBtn(text, title) {
  const safe = escHtml(text).replace(/"/g, '&quot;');
  return `<button class="copy-mini" onclick="copyText('${safe}')" title="${escHtml(title)}">
    <svg width="12" height="12" viewBox="0 0 12 12" fill="none">
      <rect x="3.5" y="3.5" width="7" height="7" rx="1.2" stroke="currentColor" stroke-width="1.2"/>
      <path d="M2.5 8.5H2a1 1 0 01-1-1V2a1 1 0 011-1h5.5a1 1 0 011 1v1" stroke="currentColor" stroke-width="1.2"/>
    </svg>
  </button>`;
}

function copyAll() {
  const drawer = document.getElementById('drawer');
  const entry  = JSON.parse(drawer.dataset.entry || 'null');
  if (!entry) return;
  copyText(JSON.stringify(entry, null, 2));
}

async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    showToast('클립보드에 복사되었습니다');
  } catch (_) {
    showToast('복사 실패 (권한을 확인하세요)');
  }
}

/* ── JSON syntax highlight ───────────────────────────────── */
function jsonHighlight(val) {
  let str;
  if (typeof val === 'string') {
    str = val;
  } else if (val === null || val === undefined) {
    return '<span class="j-null">null</span>';
  } else {
    try { str = JSON.stringify(val, null, 2); }
    catch (_) { str = String(val); }
  }
  // Parse pretty-print object
  try {
    const parsed = JSON.parse(str);
    str = JSON.stringify(parsed, null, 2);
  } catch (_) {}

  return escHtml(str).replace(
    /("(\\u[a-fA-F0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g,
    match => {
      if (/^"/.test(match)) {
        if (/:$/.test(match)) return `<span class="j-key">${match}</span>`;
        return `<span class="j-str">${match}</span>`;
      }
      if (/true|false/.test(match)) return `<span class="j-bool">${match}</span>`;
      if (/null/.test(match))       return `<span class="j-null">${match}</span>`;
      return `<span class="j-num">${match}</span>`;
    }
  );
}

/* ── Status & Error ──────────────────────────────────────── */
function setStatus(type, msg) {
  const el = document.getElementById('statusBadge');
  el.textContent = msg;
  el.className = 'statusBadge';
  if (type) el.classList.add(type);
}

function showError(msg) {
  const el = document.getElementById('errorBanner');
  el.textContent = msg;
  el.style.display = '';
}

function hideError() {
  document.getElementById('errorBanner').style.display = 'none';
}

/* ── Toast ───────────────────────────────────────────────── */
let toastTimer;
function showToast(msg) {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('show'), 2000);
}

/* ── Help modal ──────────────────────────────────────────── */
function showHelp()  { document.getElementById('helpModal').classList.add('open'); }
function closeHelp() { document.getElementById('helpModal').classList.remove('open'); }

/* ── API Guide modal ─────────────────────────────────────── */
function showApiGuide()  { document.getElementById('apiModal').classList.add('open'); }
function closeApiGuide() { document.getElementById('apiModal').classList.remove('open'); }

/* ── Mobile filter toggle ────────────────────────────────── */
function toggleMobileFilter() {
  document.getElementById('sidebar').classList.toggle('mobile-open');
}

/* ── Keyboard shortcuts ──────────────────────────────────── */
function setupKeyboard() {
  document.addEventListener('keydown', e => {
    const tag = document.activeElement?.tagName;
    const inInput = tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT';

    if (e.key === 'Escape') {
      if (document.getElementById('helpModal').classList.contains('open')) { closeHelp(); return; }
      if (document.getElementById('apiModal').classList.contains('open'))  { closeApiGuide(); return; }
      if (document.getElementById('drawer').classList.contains('open'))    { closeDrawer(); return; }
      const popup = document.getElementById('timePopup');
      if (popup && popup.classList.contains('show')) { popup.classList.remove('show'); return; }
      if (inInput) document.activeElement.blur();
      return;
    }

    if (inInput) return;

    if (e.key === '/') { e.preventDefault(); document.getElementById('keyword').focus(); return; }
    if (e.key === '?') { e.preventDefault(); showHelp(); return; }

    if (e.key === 'j' || e.key === 'ArrowDown') {
      e.preventDefault();
      const next = Math.min(selectedRow + 1, currentItems.length - 1);
      if (next >= 0) selectRow(next);
      return;
    }
    if (e.key === 'k' || e.key === 'ArrowUp') {
      e.preventDefault();
      const prev = Math.max(selectedRow - 1, 0);
      if (currentItems.length > 0) selectRow(prev);
      return;
    }
  });
}

/* ── Escape ──────────────────────────────────────────────── */
function escHtml(str) {
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}
