// app.js
//
// Boots the Uppy Dashboard on the upload page and wires it to the official
// Tus plugin. Runtime limits are fetched from /api/config so the binary stays
// the single source of truth.

import { Uppy, Dashboard, Tus, Webcam } from './vendor/uppy.min.mjs'
import { detectMobilePlatform, isMobilePlatform, uploadNoteForPlatform } from './platform.mjs'

const mobilePlatform = detectMobilePlatform()
const isMobile = isMobilePlatform()
const isIOS = mobilePlatform === 'ios'
const isAndroid = mobilePlatform === 'android'
const STORAGE_KEY = 'easy-temp-host:history'
const MAX_HISTORY = 50

init().catch((err) => {
  console.error('uppy init failed', err)
  const target = document.getElementById('uppy')
  if (target) target.textContent = '初始化上传组件失败，请刷新重试。'
})

async function init() {
  const config = await fetchConfig()
  renderMeta(config)
  loadHistory()

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
    height: isMobile ? 420 : 470,
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
    addToHistory(valid)
    renderLinks(loadHistory())
    setBatchState(fail.length ? '部分失败' : '上传完成', fail.length ? 'error' : 'complete')
  })

  uppy.on('file-added', () => setBatchState('文件已就绪'))
  uppy.on('upload', () => setBatchState('正在上传', 'uploading'))

  uppy.on('upload-error', (file, error) => {
    showToast(`「${file.name}」上传失败：${error.message}`, 'error')
  })

  document.getElementById('clear-history')?.addEventListener('click', () => {
    if (confirm('确定要清空本地上传历史吗？')) {
      localStorage.removeItem(STORAGE_KEY)
      renderLinks([])
    }
  })

  setupDragOverlay(uppy)

  window.uppy = uppy
}

// ---- drag & drop UX --------------------------------------------------------

function setupDragOverlay(uppy) {
  if (isMobile) return
  const uppyRoot = document.getElementById('uppy')
  if (!uppyRoot) return

  let dragCounter = 0
  const addFilesPanel = () => uppyRoot.querySelector('.uppy-Dashboard-AddFiles')

  const setDragging = (active) => {
    const panel = addFilesPanel()
    if (!panel) return
    if (active) {
      panel.classList.add('is-drag-target')
      panel.setAttribute('drag-state', 'over')
    } else {
      panel.classList.remove('is-drag-target')
      panel.removeAttribute('drag-state')
    }
  }

  window.addEventListener('dragenter', (e) => {
    if (!e.dataTransfer || !hasFiles(e.dataTransfer)) return
    dragCounter++
    setDragging(true)
  })

  window.addEventListener('dragleave', (e) => {
    if (!e.dataTransfer || !hasFiles(e.dataTransfer)) return
    dragCounter--
    if (dragCounter <= 0) {
      dragCounter = 0
      setDragging(false)
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
      setDragging(false)
    }
  })
}

function hasFiles(dt) {
  if (!dt.types) return false
  return Array.from(dt.types).some((t) => t === 'Files' || t === 'application/x-moz-file')
}

// ---- config & meta --------------------------------------------------------

async function fetchConfig() {
  try {
    const res = await fetch('/api/config')
    if (res.ok) return await res.json()
  } catch (e) {
    // fall through to defaults
  }
  return {
    maxFileSize: 10 * 1024 * 1024 * 1024,
    chunkSize: 8 * 1024 * 1024,
    allowedTypes: 'all',
    retention: '1d',
  }
}

function renderMeta(cfg) {
  const set = (id, text) => {
    const el = document.getElementById(id)
    if (el) el.innerHTML = text
  }
  set('pill-maxsize', iconPill('单文件上限', formatBytes(cfg.maxFileSize)))
  set('pill-chunk', iconPill('分片大小', formatBytes(cfg.chunkSize)))
  set('pill-retention', iconPill('保留', cfg.retention || '1d'))
  set('pill-types', iconPill('允许类型', cfg.allowedTypes || 'all'))
}

function iconPill(label, value) {
  return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="10"/></svg>${label} <strong>${escapeHtml(value)}</strong>`
}

function setBatchState(text, tone = '') {
  const batch = document.getElementById('batch-state')
  const service = document.getElementById('service-state')
  if (!batch || !service) return
  batch.textContent = text
  batch.className = `state-chip${tone ? ` is-${tone}` : ''}`
  const label = service.querySelector('span:last-child')
  if (label) label.textContent = text
}

// ---- helpers ---------------------------------------------------------------

function formatBytes(n) {
  if (!n && n !== 0) return '-'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

function allowedFileTypes(raw) {
  if (!raw || raw === 'all') return null
  const groups = {
    images: ['image/jpeg', 'image/png', 'image/gif', 'image/webp'],
    videos: ['video/mp4', 'video/webm', 'video/quicktime', 'video/x-matroska', 'video/x-msvideo', 'video/mpeg'],
    audio: ['audio/mpeg', 'audio/ogg', 'audio/wav', 'audio/webm', 'audio/aac', 'audio/flac'],
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
    console.error('fetch completed upload URL failed', error)
    return null
  }
}

// ---- history ---------------------------------------------------------------

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

function addToHistory(urls) {
  if (!urls.length) return
  const existing = loadHistory()
  const now = Date.now()
  const entries = urls.map((url) => ({ url, createdAt: now }))
  const seen = new Set()
  const merged = []
  for (const item of [...entries, ...existing]) {
    if (seen.has(item.url)) continue
    seen.add(item.url)
    merged.push(item)
  }
  saveHistory(merged)
}

// ---- links UI --------------------------------------------------------------

function renderLinks(items) {
  const panel = document.getElementById('links')
  if (!panel) return
  const list = document.getElementById('link-list')
  if (!items.length) {
    panel.hidden = true
    return
  }
  panel.hidden = false
  list.innerHTML = ''
  for (const item of items) {
    const url = typeof item === 'string' ? item : item.url
    const createdAt = typeof item === 'string' ? null : item.createdAt
    const li = document.createElement('li')
    li.className = 'link-row'

    const input = document.createElement('input')
    input.type = 'text'
    input.value = url
    input.readOnly = true

    const meta = document.createElement('span')
    meta.className = 'link-meta'
    if (createdAt) {
      meta.textContent = formatTimeAgo(createdAt)
    }

    const copy = document.createElement('button')
    copy.type = 'button'
    copy.className = 'copy-btn'
    copy.innerHTML = copyIcon('复制')
    copy.onclick = async () => {
      try { await navigator.clipboard.writeText(url) }
      catch { input.select(); document.execCommand('copy') }
      copy.classList.add('copied')
      copy.innerHTML = checkIcon('已复制')
      setTimeout(() => {
        copy.classList.remove('copied')
        copy.innerHTML = copyIcon('复制')
      }, 1800)
    }

    const open = document.createElement('a')
    open.href = url
    open.target = '_blank'
    open.rel = 'noopener'
    open.className = 'open-btn'
    open.innerHTML = externalIcon('打开')

    li.append(input, meta, copy, open)
    list.appendChild(li)
  }
}

function copyIcon(text) {
  return `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>${escapeHtml(text)}`
}

function checkIcon(text) {
  return `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="20 6 9 17 4 12"/></svg>${escapeHtml(text)}`
}

function externalIcon(text) {
  return `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/></svg>${escapeHtml(text)}`
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

// ---- toast -----------------------------------------------------------------

function showToast(message, type = 'info') {
  const existing = document.querySelector('.toast')
  if (existing) existing.remove()
  const toast = document.createElement('div')
  toast.className = `toast ${type}`
  toast.textContent = message
  document.body.appendChild(toast)
  setTimeout(() => toast.remove(), 4000)
}

// ---- locale ----------------------------------------------------------------

function zhLocale() {
  return {
    strings: {
      browse: '选择文件',
      browseFiles: '浏览文件',
      dropPasteFiles: '拖拽文件到这里，或 %{browse}',
      dropHint: '拖放文件到这里上传',
      upload: '上传',
      uploadXFiles: '上传 %{smart_count} 个文件',
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
      pluginNameCamera: '从相机导入',
      noCameraTitle: '未检测到摄像头',
      noCameraDescription: '请连接摄像头设备后再拍照或录制视频',
      takePicture: '拍照',
      recordVideo: '录像',
      recordAudio: '录音',
    },
  }
}
