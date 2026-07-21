// app.js
// Modernized Client logic for easy-temp-cloud with enhanced PC & Mobile (iOS/Android) interaction.

import { Uppy, Dashboard, Tus, Webcam } from './vendor/uppy.min.mjs'
import { detectMobilePlatform, isMobilePlatform, uploadNoteForPlatform } from './platform.mjs'
import { generateQRCodeSVG } from './qrcode.mjs'

const mobilePlatform = detectMobilePlatform()
const isMobile = isMobilePlatform()
const isIOS = mobilePlatform === 'ios'
const isAndroid = mobilePlatform === 'android'
const STORAGE_KEY = 'easy-temp-cloud:history'
const MAX_HISTORY = 50

init().catch((err) => {
  console.error('Uppy initialization failed', err)
  showToast('初始化上传组件失败，请刷新页面重试', 'error')
})

async function init() {
  const config = await fetchConfig()
  renderMeta(config)
  setupTabs()
  updateHistoryBadge()
  renderLinks(loadHistory())

  const uppy = new Uppy({
    debug: false,
    autoProceed: false,
    allowMultipleUploadBatches: true,
    logger: console,
    restrictions: {
      maxFileSize: config.maxFileSize || 10 * 1024 * 1024 * 1024,
      maxNumberOfFiles: 200,
      minNumberOfFiles: 1,
      allowedFileTypes: allowedFileTypes(config.allowedTypes),
    },
    locale: zhLocale(),
  })

  uppy.use(Dashboard, {
    inline: true,
    target: '#uppy',
    theme: 'light',
    showProgressDetails: true,
    showLinkToFileUploadResult: false,
    proudlyDisplayPoweredByUppy: false,
    height: isMobile ? 380 : 440,
    width: '100%',
    note: uploadNoteForPlatform(mobilePlatform),
  })

  if (isMobile) {
    uppy.use(Webcam, {
      target: Dashboard,
      modes: ['picture', 'video-audio'],
      mobileNativeCamera: isIOS || isAndroid || isMobile,
      showRecordingLength: true,
      mirror: true,
    })
  }

  uppy.use(Tus, {
    endpoint: '/api/uploads/',
    chunkSize: config.chunkSize || 8 * 1024 * 1024,
    limit: isMobile ? 2 : 3,
    retryDelays: [0, 1000, 3000, 8000],
    storeFingerprintForResuming: true,
    removeFingerprintOnSuccess: true,
  })

  uppy.on('complete', async (result) => {
    const ok = result.successful || []
    const fail = result.failed || []
    if (ok.length) showToast(`成功上传 ${ok.length} 个文件`, 'success')
    if (fail.length) showToast(`${fail.length} 个文件上传失败`, 'error')

    const urls = await Promise.all(ok.map(resolvePublicURL))
    const valid = urls.filter(Boolean)
    
    // Add to history with file meta
    const items = ok.map((file, idx) => ({
      url: valid[idx],
      name: file.name,
      size: file.size,
      type: file.type,
      createdAt: Date.now(),
    })).filter((item) => item.url)

    addToHistory(items)
    updateHistoryBadge()
    renderLinks(loadHistory())
    setBatchState(fail.length ? '部分失败' : '上传完成', fail.length ? 'error' : 'complete')

    // Auto switch to history tab or notify if multiple files uploaded
    if (valid.length > 0) {
      setTimeout(() => switchTab('history'), 800)
    }
  })

  uppy.on('file-added', () => setBatchState('文件已就绪'))
  uppy.on('upload', () => setBatchState('正在上传中...', 'uploading'))
  uppy.on('upload-error', (file, error) => {
    showToast(`「${file.name}」上传失败：${error.message}`, 'error')
  })

  setupFullWindowDrag(uppy)
  setupClipboardPaste(uppy)
  setupGlobalActions()

  window.uppy = uppy
}

// ---- Full-Window Drag & Drop (PC) ------------------------------------------

function setupFullWindowDrag(uppy) {
  if (isMobile) return
  const overlay = document.getElementById('drag-overlay')
  if (!overlay) return

  let dragCounter = 0

  window.addEventListener('dragenter', (e) => {
    if (!e.dataTransfer || !hasFiles(e.dataTransfer)) return
    dragCounter++
    overlay.classList.add('is-active')
  })

  window.addEventListener('dragleave', (e) => {
    if (!e.dataTransfer || !hasFiles(e.dataTransfer)) return
    dragCounter--
    if (dragCounter <= 0) {
      dragCounter = 0
      overlay.classList.remove('is-active')
    }
  })

  window.addEventListener('dragover', (e) => {
    if (e.dataTransfer && hasFiles(e.dataTransfer)) {
      e.preventDefault()
      e.dataTransfer.dropEffect = 'copy'
    }
  })

  window.addEventListener('drop', (e) => {
    if (e.dataTransfer && hasFiles(e.dataTransfer)) {
      e.preventDefault()
      dragCounter = 0
      overlay.classList.remove('is-active')
      const files = Array.from(e.dataTransfer.files)
      files.forEach((file) => {
        try {
          uppy.addFile({
            name: file.name,
            type: file.type,
            data: file,
            source: 'drag-and-drop',
          })
        } catch (err) {
          console.warn('Failed to add dropped file', err)
        }
      })
      switchTab('upload')
    }
  })
}

function hasFiles(dt) {
  if (!dt.types) return false
  return Array.from(dt.types).some((t) => t === 'Files' || t === 'application/x-moz-file')
}

// ---- Clipboard Paste Support (Ctrl+V / Cmd+V) -----------------------------

function setupClipboardPaste(uppy) {
  window.addEventListener('paste', (e) => {
    const items = (e.clipboardData || e.originalEvent.clipboardData)?.items
    if (!items) return

    let added = 0
    for (const item of items) {
      if (item.kind === 'file') {
        const file = item.getAsFile()
        if (!file) continue
        const filename = file.name && file.name !== 'image.png' ? file.name : `paste-${Date.now()}.${file.type.split('/')[1] || 'bin'}`
        try {
          uppy.addFile({
            name: filename,
            type: file.type,
            data: file,
            source: 'clipboard-paste',
          })
          added++
        } catch (err) {
          console.warn('Failed to add pasted file', err)
        }
      }
    }
    if (added > 0) {
      showToast(`已成功粘贴 ${added} 个剪贴板文件`, 'info')
      switchTab('upload')
    }
  })
}

// ---- Config & Meta --------------------------------------------------------

async function fetchConfig() {
  try {
    const res = await fetch('/api/config')
    if (res.ok) return await res.json()
  } catch (e) {
    // fallback
  }
  return {
    maxFileSize: 10 * 1024 * 1024 * 1024,
    chunkSize: 8 * 1024 * 1024,
    allowedTypes: 'all',
    retention: '1d',
  }
}

function renderMeta(cfg) {
  const set = (id, html) => {
    const el = document.getElementById(id)
    if (el) el.innerHTML = html
  }
  set('pill-maxsize', iconPill('单文件上限', formatBytes(cfg.maxFileSize)))
  set('pill-chunk', iconPill('分片大小', formatBytes(cfg.chunkSize)))
  set('pill-retention', iconPill('保留时间', cfg.retention || '1d'))
  set('pill-types', iconPill('允许格式', cfg.allowedTypes || '全部格式'))
}

function iconPill(label, value) {
  return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>${label} <strong>${escapeHtml(value)}</strong>`
}

function setBatchState(text, tone = '') {
  const batch = document.getElementById('batch-state')
  const service = document.getElementById('service-label')
  if (batch) {
    batch.textContent = text
    batch.className = `pill${tone ? ` state-${tone}` : ''}`
  }
  if (service) service.textContent = text
}

// ---- Navigation Tabs ------------------------------------------------------

function setupTabs() {
  const tabs = document.querySelectorAll('.tab-btn')
  tabs.forEach((tab) => {
    tab.addEventListener('click', () => {
      const targetTab = tab.getAttribute('data-tab')
      switchTab(targetTab)
    })
  })
}

function switchTab(tabName) {
  const tabs = document.querySelectorAll('.tab-btn')
  const uploadSection = document.getElementById('upload-section')
  const historySection = document.getElementById('history-section')

  tabs.forEach((t) => {
    if (t.getAttribute('data-tab') === tabName) {
      t.classList.add('is-active')
    } else {
      t.classList.remove('is-active')
    }
  })

  if (tabName === 'upload') {
    uploadSection.hidden = false
    historySection.hidden = true
  } else {
    uploadSection.hidden = true
    historySection.hidden = false
  }
}

function updateHistoryBadge() {
  const countEl = document.getElementById('history-count')
  if (!countEl) return
  const history = loadHistory()
  countEl.textContent = history.length
}

// ---- Links & History UI ---------------------------------------------------

function renderLinks(items) {
  const list = document.getElementById('link-list')
  if (!list) return

  if (!items || !items.length) {
    list.innerHTML = `
      <div class="empty-state">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/>
          <polyline points="13 2 13 9 20 9"/>
        </svg>
        <p>暂无文件传输历史，拖拽或选择文件上传后将在此生成分享链接。</p>
      </div>
    `
    return
  }

  list.innerHTML = ''
  for (const item of items) {
    const url = typeof item === 'string' ? item : item.url
    const name = typeof item === 'object' ? item.name : ''
    const size = typeof item === 'object' ? item.size : null
    const createdAt = typeof item === 'object' ? item.createdAt : null
    const type = typeof item === 'object' ? item.type : ''

    const li = document.createElement('li')
    li.className = 'link-row'

    // Icon
    const iconDiv = document.createElement('div')
    iconDiv.className = 'file-type-icon'
    iconDiv.innerHTML = getFileTypeIcon(name, type)

    // Info Wrap
    const infoDiv = document.createElement('div')
    infoDiv.className = 'link-info'

    const input = document.createElement('input')
    input.type = 'text'
    input.className = 'link-url-input'
    input.value = url
    input.readOnly = true
    input.onclick = () => input.select()

    const metaSpan = document.createElement('span')
    metaSpan.className = 'link-meta-text'
    let metaText = ''
    if (name) metaText += escapeHtml(name)
    if (size) metaText += ` (${formatBytes(size)})`
    if (createdAt) metaText += ` · ${formatTimeAgo(createdAt)}`
    metaSpan.textContent = metaText || url

    infoDiv.append(input, metaSpan)

    // Actions Wrap
    const actionsDiv = document.createElement('div')
    actionsDiv.className = 'link-actions'

    // Copy Button
    const copyBtn = document.createElement('button')
    copyBtn.type = 'button'
    copyBtn.className = 'btn btn-sm'
    copyBtn.innerHTML = `${svgIcon('copy')} 复制`
    copyBtn.onclick = () => copyToClipboard(url, copyBtn)

    // Native Share or QR Code Button
    const shareBtn = document.createElement('button')
    shareBtn.type = 'button'
    shareBtn.className = 'btn btn-sm'
    if (navigator.share) {
      shareBtn.innerHTML = `${svgIcon('share')} 分享`
      shareBtn.onclick = () => {
        navigator.share({
          title: name || 'easy-temp-cloud 临时文件',
          text: `从 easy-temp-cloud 共享的文件 ${name || ''}`,
          url: url,
        }).catch(() => {})
      }
    } else {
      shareBtn.innerHTML = `${svgIcon('qr')} 二维码`
      shareBtn.onclick = () => showQRModal(url)
    }

    // QR Code Button (if native share was shown)
    let qrBtn = null
    if (navigator.share) {
      qrBtn = document.createElement('button')
      qrBtn.type = 'button'
      qrBtn.className = 'btn btn-sm'
      qrBtn.innerHTML = `${svgIcon('qr')}`
      qrBtn.title = '查看二维码'
      qrBtn.onclick = () => showQRModal(url)
    }

    // Open Button
    const openLink = document.createElement('a')
    openLink.href = url
    openLink.target = '_blank'
    openLink.rel = 'noopener'
    openLink.className = 'btn btn-sm'
    openLink.innerHTML = `${svgIcon('external')}`
    openLink.title = '新窗口打开'

    // Delete Item Button
    const delBtn = document.createElement('button')
    delBtn.type = 'button'
    delBtn.className = 'btn btn-sm btn-danger-ghost'
    delBtn.innerHTML = `${svgIcon('trash')}`
    delBtn.title = '从列表中删除'
    delBtn.onclick = () => {
      deleteFromHistory(url)
      updateHistoryBadge()
      renderLinks(loadHistory())
      showToast('已移除该记录', 'info')
    }

    actionsDiv.append(copyBtn, shareBtn)
    if (qrBtn) actionsDiv.append(qrBtn)
    actionsDiv.append(openLink, delBtn)

    li.append(iconDiv, infoDiv, actionsDiv)
    list.appendChild(li)
  }
}

// ---- Global Actions (Copy All, Clear History, QR Modal) --------------------

function setupGlobalActions() {
  // Clear History
  document.getElementById('clear-history-btn')?.addEventListener('click', () => {
    if (confirm('确定要清空全部上传历史记录吗？')) {
      localStorage.removeItem(STORAGE_KEY)
      updateHistoryBadge()
      renderLinks([])
      showToast('历史记录已清空', 'info')
    }
  })

  // Copy All Links
  document.getElementById('copy-all-btn')?.addEventListener('click', () => {
    const history = loadHistory()
    if (!history.length) {
      showToast('暂无可复制的历史链接', 'info')
      return
    }
    const allUrls = history.map((item) => (typeof item === 'string' ? item : item.url)).join('\n')
    copyToClipboard(allUrls)
    showToast(`已成功复制 ${history.length} 条链接到剪贴板`, 'success')
  })

  // Close QR Modal
  const modal = document.getElementById('qr-modal')
  const closeBtn = document.getElementById('qr-close-btn')
  const copyBtn = document.getElementById('qr-copy-btn')

  closeBtn?.addEventListener('click', hideQRModal)
  modal?.addEventListener('click', (e) => {
    if (e.target === modal) hideQRModal()
  })

  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') hideQRModal()
  })

  copyBtn?.addEventListener('click', () => {
    const text = document.getElementById('qr-url-text')?.textContent
    if (text) {
      copyToClipboard(text)
      showToast('已复制链接', 'success')
      hideQRModal()
    }
  })
}

function showQRModal(url) {
  const modal = document.getElementById('qr-modal')
  const qrBody = document.getElementById('qr-code-body')
  const qrText = document.getElementById('qr-url-text')
  if (!modal || !qrBody || !qrText) return

  qrBody.innerHTML = generateQRCodeSVG(url, { size: 200 })
  qrText.textContent = url
  modal.classList.add('is-active')
  modal.setAttribute('aria-hidden', 'false')
}

function hideQRModal() {
  const modal = document.getElementById('qr-modal')
  if (!modal) return
  modal.classList.remove('is-active')
  modal.setAttribute('aria-hidden', 'true')
}

// ---- LocalStorage History Management --------------------------------------

function loadHistory() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    return raw ? JSON.parse(raw) : []
  } catch {
    return []
  }
}

function saveHistory(items) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(items.slice(0, MAX_HISTORY)))
  } catch {
    // ignore
  }
}

function addToHistory(newItems) {
  if (!newItems || !newItems.length) return
  const existing = loadHistory()
  const seen = new Set()
  const merged = []

  for (const item of [...newItems, ...existing]) {
    const u = typeof item === 'string' ? item : item.url
    if (seen.has(u)) continue
    seen.add(u)
    merged.push(item)
  }
  saveHistory(merged)
}

function deleteFromHistory(urlToDelete) {
  const existing = loadHistory()
  const filtered = existing.filter((item) => {
    const u = typeof item === 'string' ? item : item.url
    return u !== urlToDelete
  })
  saveHistory(filtered)
}

// ---- Toast System ---------------------------------------------------------

function showToast(message, type = 'info') {
  const container = document.getElementById('toast-container') || document.body
  const toast = document.createElement('div')
  toast.className = `toast ${type}`
  toast.textContent = message

  container.appendChild(toast)
  setTimeout(() => {
    toast.style.opacity = '0'
    toast.style.transform = 'translateY(-10px)'
    setTimeout(() => toast.remove(), 250)
  }, 3500)
}

// ---- Copy Helper ----------------------------------------------------------

async function copyToClipboard(text, btnElement = null) {
  try {
    await navigator.clipboard.writeText(text)
    showToast('已成功复制到剪贴板', 'success')
  } catch {
    const input = document.createElement('textarea')
    input.value = text
    document.body.appendChild(input)
    input.select()
    document.execCommand('copy')
    document.body.removeChild(input)
    showToast('已成功复制到剪贴板', 'success')
  }

  if (btnElement) {
    const oldHTML = btnElement.innerHTML
    btnElement.innerHTML = `${svgIcon('check')} 已复制`
    btnElement.style.color = 'var(--success)'
    setTimeout(() => {
      btnElement.innerHTML = oldHTML
      btnElement.style.color = ''
    }, 1800)
  }
}

// ---- Icon Helpers ---------------------------------------------------------

function getFileTypeIcon(filename = '', mimeType = '') {
  const ext = filename.split('.').pop()?.toLowerCase() || ''
  if (/image|png|jpg|jpeg|gif|webp|svg/i.test(mimeType) || ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(ext)) {
    return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2" ry="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/></svg>`
  }
  if (/video|mp4|webm|mkv|avi/i.test(mimeType) || ['mp4', 'webm', 'mkv', 'mov', 'avi'].includes(ext)) {
    return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="23 7 16 12 23 17 23 7"/><rect x="1" y="5" width="15" height="14" rx="2" ry="2"/></svg>`
  }
  if (/audio|mp3|wav|flac|ogg/i.test(mimeType) || ['mp3', 'wav', 'flac', 'm4a', 'aac'].includes(ext)) {
    return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>`
  }
  if (['zip', 'rar', '7z', 'tar', 'gz'].includes(ext)) {
    return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 8v13H3V3h10l8 5z"/><path d="M10 12h4"/><path d="M10 16h4"/></svg>`
  }
  return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>`
}

function svgIcon(name) {
  switch (name) {
    case 'copy':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`
    case 'check':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>`
    case 'share':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/><line x1="8.59" y1="13.51" x2="15.42" y2="17.49"/><line x1="15.41" y1="6.51" x2="8.59" y2="10.49"/></svg>`
    case 'qr':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/></svg>`
    case 'external':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/></svg>`
    case 'trash':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>`
    default:
      return ''
  }
}

function formatBytes(n) {
  if (!n && n !== 0) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

function allowedFileTypes(raw) {
  if (!raw || raw === 'all') return null
  const groups = {
    images: ['image/jpeg', 'image/png', 'image/gif', 'image/webp'],
    videos: ['video/mp4', 'video/webm', 'video/quicktime', 'video/x-matroska'],
    audio: ['audio/mpeg', 'audio/ogg', 'audio/wav', 'audio/aac', 'audio/flac'],
    docs: ['application/pdf', 'text/plain', 'text/markdown', 'text/html'],
  }
  const out = new Set()
  for (const token of raw.split(',')) {
    const t = token.trim()
    if (!t) continue
    if (groups[t]) { groups[t].forEach((m) => out.add(m)); continue }
    out.add(t)
  }
  return out.size ? [...out] : null
}

function escapeHtml(value) {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

async function resolvePublicURL(file) {
  const uploadURL = (file.response && file.response.uploadURL) || file.uploadURL
  if (!uploadURL) return null
  try {
    const response = await fetch(`${uploadURL.replace(/\/$/, '')}/result`)
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    const result = await response.json()
    return result.url || null
  } catch (error) {
    console.error('Fetch completed upload URL failed', error)
    return null
  }
}

function formatTimeAgo(ts) {
  const seconds = Math.floor((Date.now() - ts) / 1000)
  if (seconds < 60) return '刚刚'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  const days = Math.floor(hours / 24)
  return `${days} 天前`
}

function zhLocale() {
  return {
    strings: {
      browse: '选择文件',
      browseFiles: '浏览文件',
      dropPasteFiles: '拖拽文件到这里，或 %{browse}',
      dropHint: '拖放文件到这里上传',
      upload: '开始上传',
      uploadXFiles: '开始上传 %{smart_count} 个文件',
      uploadXNewFiles: '上传 %{smart_count} 个新文件',
      pauseUpload: '暂停上传',
      resumeUpload: '继续上传',
      retryUpload: '重试',
      cancel: '取消',
      done: '完成',
      xFilesSelected: '已选择 %{smart_count} 个文件',
      xTimeLeft: '还剩 %{time}',
      uploadComplete: '上传完成',
      uploadFailed: '上传失败',
      pleaseTryAgain: '请重试',
      myDevice: '本机文件',
      pluginNameCamera: '拍照 / 录像上传',
      noCameraTitle: '未检测到摄像头',
      noCameraDescription: '请连接摄像头设备后再拍照或录制视频',
      recordingStoppedMaxSize: '录制已停止，文件大小即超过限制',
      submitRecordedFile: '使用此文件',
      discardRecordedFile: '重新拍摄',
      smile: '请看镜头',
      takePicture: '拍照',
      startRecording: '开始录像',
      stopRecording: '停止录像',
      recordVideo: '录像',
      recordAudio: '录音',
      recordingLength: '录制时长 %{recording_length}',
      allowAccessTitle: '请允许访问相机',
      allowAccessDescription: '拍照或录制视频前，请允许此站点访问你的相机。',
    },
  }
}
