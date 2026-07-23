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
const MAX_THUMBNAIL_DATA_URL_LENGTH = 48 * 1024
let activeDeleteItem = null

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

    const [results, thumbnails] = await Promise.all([
      Promise.all(ok.map(resolvePublicURL)),
      createHistoryThumbnails(ok),
    ])
    
    // Add to history with file meta
    const items = ok.map((file, idx) => {
      const result = results[idx]
      if (!result || !result.url) return null
      return {
        url: result.url,
        id: result.id,
        name: file.name,
        size: file.size,
        type: file.type,
        thumbnail: thumbnails[idx],
        createdAt: Date.now(),
      }
    }).filter(Boolean)

    addToHistory(items)
    updateHistoryBadge()
    renderLinks(loadHistory())
    setBatchState(fail.length ? '部分失败' : '上传完成', fail.length ? 'error' : 'complete')

    // Auto switch to history tab or notify if multiple files uploaded
    if (items.length > 0) {
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
  setupFileManager()

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
  set('pill-retention', iconPill('保留时间', formatRetention(cfg.retention || '1d')))
  set('pill-types', iconPill('允许格式', cfg.allowedTypes || '全部格式'))
  renderClientAPI(cfg.apiPassword)
}

function renderClientAPI(password) {
  const row = document.getElementById('client-api')
  const input = document.getElementById('client-api-url')
  if (!row || !input) return

  if (typeof password !== 'string' || !password) {
    row.hidden = true
    input.value = ''
    return
  }

  input.value = `${window.location.origin}/api/upload?pwd=${encodeURIComponent(password)}`
  row.hidden = false
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
  tabs.forEach((t) => {
    if (t.getAttribute('data-tab') === tabName) {
      t.classList.add('is-active')
    } else {
      t.classList.remove('is-active')
    }
  })

  // Show the matching section, hide the others.
  const main = document.querySelector('.app-main')
  main.querySelectorAll('section.card').forEach((section) => {
    const id = section.id || ''
    const name = id.replace(/-section$/, '')
    section.hidden = name !== tabName
  })

  // Lazy-load the file manager list the first time the tab is opened.
  if (tabName === 'manage') {
    refreshFileManager()
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
    const iconDiv = createHistoryPreview(item, name, type)

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
    delBtn.onclick = () => showDeleteLinkModal(item)

    actionsDiv.append(copyBtn, shareBtn)
    if (qrBtn) actionsDiv.append(qrBtn)
    actionsDiv.append(openLink, delBtn)

    li.append(iconDiv, infoDiv, actionsDiv)
    list.appendChild(li)
  }
}

// ---- Global Actions (Copy All, Clear History, QR Modal) --------------------

function setupGlobalActions() {

  document.getElementById('copy-api-url-btn')?.addEventListener('click', () => {
    const apiURL = document.getElementById('client-api-url')?.value
    if (apiURL) copyToClipboard(apiURL)
  })

  // Clear History Modal
  const clearModal = document.getElementById('clear-history-modal')
  const clearBtn = document.getElementById('clear-history-btn')
  const clearCancelBtn = document.getElementById('clear-history-cancel-btn')
  const clearConfirmBtn = document.getElementById('clear-history-confirm-btn')

  function showClearHistoryModal() {
    if (!clearModal) return
    clearModal.classList.add('is-active')
    clearModal.setAttribute('aria-hidden', 'false')
  }

  function hideClearHistoryModal() {
    if (!clearModal) return
    clearModal.classList.remove('is-active')
    clearModal.setAttribute('aria-hidden', 'true')
  }

  clearBtn?.addEventListener('click', showClearHistoryModal)
  clearCancelBtn?.addEventListener('click', hideClearHistoryModal)
  clearModal?.addEventListener('click', (e) => {
    if (e.target === clearModal) hideClearHistoryModal()
  })
  clearConfirmBtn?.addEventListener('click', () => {
    localStorage.removeItem(STORAGE_KEY)
    updateHistoryBadge()
    renderLinks([])
    hideClearHistoryModal()
    showToast('历史记录已清空', 'info')
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

  copyBtn?.addEventListener('click', () => {
    const text = document.getElementById('qr-url-text')?.textContent
    if (text) {
      copyToClipboard(text)
      showToast('已复制链接', 'success')
      hideQRModal()
    }
  })

  const deleteModal = document.getElementById('delete-link-modal')
  const deleteCancelBtn = document.getElementById('delete-link-cancel-btn')
  const deleteConfirmBtn = document.getElementById('delete-link-confirm-btn')
  deleteCancelBtn?.addEventListener('click', hideDeleteLinkModal)
  deleteModal?.addEventListener('click', (e) => {
    if (e.target === deleteModal) hideDeleteLinkModal()
  })
  deleteConfirmBtn?.addEventListener('click', confirmDeleteLink)

  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') hideDeleteLinkModal()
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

function showDeleteLinkModal(item) {
  const modal = document.getElementById('delete-link-modal')
  const name = document.getElementById('delete-link-name')
  const checkbox = document.getElementById('delete-resource-checkbox')
  const hint = document.getElementById('delete-resource-hint')
  const confirmBtn = document.getElementById('delete-link-confirm-btn')
  if (!modal || !name || !checkbox || !hint || !confirmBtn) return

  activeDeleteItem = item
  const resourceID = getResourceID(item)
  const itemName = typeof item === 'object' && item.name ? item.name : getHistoryURL(item)
  name.textContent = itemName
  checkbox.checked = false
  checkbox.disabled = !resourceID
  hint.textContent = resourceID
    ? '删除后分享链接将立即失效，且无法恢复。'
    : '此历史记录缺少资源标识，只能从当前浏览器移除。'
  confirmBtn.disabled = false
  modal.classList.add('is-active')
  modal.setAttribute('aria-hidden', 'false')
}

function hideDeleteLinkModal() {
  const modal = document.getElementById('delete-link-modal')
  if (!modal) return
  modal.classList.remove('is-active')
  modal.setAttribute('aria-hidden', 'true')
  activeDeleteItem = null
}

async function confirmDeleteLink() {
  const item = activeDeleteItem
  const checkbox = document.getElementById('delete-resource-checkbox')
  const confirmBtn = document.getElementById('delete-link-confirm-btn')
  if (!item || !checkbox || !confirmBtn) return

  const url = getHistoryURL(item)
  const resourceID = getResourceID(item)
  const deleteResource = checkbox.checked && resourceID
  confirmBtn.disabled = true
  try {
    if (deleteResource) {
      const response = await fetch(`/api/files/${encodeURIComponent(resourceID)}`, {
        method: 'DELETE',
        credentials: 'same-origin',
      })
      if (!response.ok && response.status !== 404) {
        const body = await response.json().catch(() => ({}))
        throw new Error(body.error || `HTTP ${response.status}`)
      }
    }
    deleteFromHistory(url)
    updateHistoryBadge()
    renderLinks(loadHistory())
    hideDeleteLinkModal()
    showToast(deleteResource ? '资源文件和历史记录已删除' : '已移除该记录', 'info')
  } catch (error) {
    console.error('Delete shared file failed', error)
    showToast(`删除资源文件失败：${error.message}`, 'error')
    confirmBtn.disabled = false
  }
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

function getHistoryURL(item) {
  return typeof item === 'string' ? item : item.url
}

function getResourceID(item) {
  if (typeof item === 'object' && /^[a-f0-9]{64}$/.test(item.id || '')) return item.id
  try {
    const url = new URL(getHistoryURL(item), window.location.href)
    const match = url.origin === window.location.origin && url.pathname.match(/^\/files\/([a-f0-9]{64})$/)
    return match ? match[1] : ''
  } catch {
    return ''
  }
}

async function createHistoryThumbnails(files) {
  const thumbnails = new Array(files.length).fill('')
  let nextIndex = 0
  const workers = Array.from({ length: Math.min(2, files.length) }, async () => {
    while (nextIndex < files.length) {
      const index = nextIndex++
      thumbnails[index] = await createHistoryThumbnail(files[index])
    }
  })
  await Promise.all(workers)
  return thumbnails
}

async function createHistoryThumbnail(file) {
  const blob = file?.data
  const type = file?.type || blob?.type || ''
  if (!(blob instanceof Blob)) return ''

  try {
    if (type.startsWith('image/')) return await createImageThumbnail(blob)
    if (type.startsWith('video/')) return await createVideoThumbnail(blob)
  } catch (error) {
    console.debug('Could not generate history thumbnail', error)
  }
  return ''
}

async function createImageThumbnail(blob) {
  const url = URL.createObjectURL(blob)
  try {
    const image = new Image()
    image.src = url
    if (image.decode) {
      await image.decode()
    } else {
      await new Promise((resolve, reject) => {
        image.onload = resolve
        image.onerror = () => reject(new Error('image unavailable'))
      })
    }
    return renderThumbnail(image, image.naturalWidth, image.naturalHeight)
  } finally {
    URL.revokeObjectURL(url)
  }
}

async function createVideoThumbnail(blob) {
  const url = URL.createObjectURL(blob)
  const video = document.createElement('video')
  video.muted = true
  video.playsInline = true
  video.preload = 'metadata'
  video.src = url
  try {
    await new Promise((resolve, reject) => {
      video.onloadeddata = resolve
      video.onerror = () => reject(new Error('video metadata unavailable'))
    })
    if (Number.isFinite(video.duration) && video.duration > 0) {
      video.currentTime = Math.min(0.5, video.duration / 2)
      await new Promise((resolve, reject) => {
        video.onseeked = resolve
        video.onerror = () => reject(new Error('video frame unavailable'))
      })
    }
    return renderThumbnail(video, video.videoWidth, video.videoHeight)
  } finally {
    video.removeAttribute('src')
    video.load()
    URL.revokeObjectURL(url)
  }
}

function renderThumbnail(source, width, height) {
  if (!width || !height) return ''
  const canvas = document.createElement('canvas')
  const context = canvas.getContext('2d')
  if (!context) return ''

  const render = (maxWidth, maxHeight, quality) => {
    const scale = Math.min(maxWidth / width, maxHeight / height, 1)
    canvas.width = Math.max(1, Math.round(width * scale))
    canvas.height = Math.max(1, Math.round(height * scale))
    context.drawImage(source, 0, 0, canvas.width, canvas.height)
    return canvas.toDataURL('image/jpeg', quality)
  }

  let thumbnail = render(160, 112, 0.72)
  if (thumbnail.length > MAX_THUMBNAIL_DATA_URL_LENGTH) {
    thumbnail = render(96, 68, 0.6)
  }
  return thumbnail.length <= MAX_THUMBNAIL_DATA_URL_LENGTH ? thumbnail : ''
}

function createHistoryPreview(item, name, type) {
  const preview = typeof item === 'object' ? item.thumbnail : ''
  const iconDiv = document.createElement('div')
  iconDiv.className = 'file-type-icon'
  if (!isSafeThumbnail(preview)) {
    iconDiv.innerHTML = getFileTypeIcon(name, type)
    return iconDiv
  }

  const image = document.createElement('img')
  image.className = 'history-thumbnail'
  image.src = preview
  image.alt = `${name || '文件'}缩略图`
  iconDiv.classList.add('has-thumbnail')
  iconDiv.appendChild(image)
  if (type.startsWith('video/')) {
    const badge = document.createElement('span')
    badge.className = 'history-video-badge'
    badge.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 7 8 5-8 5z"/></svg>'
    iconDiv.appendChild(badge)
  }
  return iconDiv
}

function isSafeThumbnail(value) {
  return typeof value === 'string' && value.length <= MAX_THUMBNAIL_DATA_URL_LENGTH && /^data:image\/(jpeg|png|webp);base64,/.test(value)
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
    case 'download':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>`
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

function formatRetention(raw) {
  const m = String(raw || '').match(/^(\d+)([mhdw])$/)
  if (!m) return String(raw || '-')
  const [, n, unit] = m
  const map = { m: '分钟', h: '小时', d: '天', w: '周' }
  return `${n} ${map[unit]}`
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
    return result.url ? { url: result.url, id: result.id || '' } : null
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

// ---- File Manager: server file list, multi-select, lasso, batch ops --------

// ---- File Manager: server file list, search, sort, multi-select, batch ops ----

let activeDeleteFileIds = []

const fileManager = {
  files: [],
  selected: new Set(),
  lastAnchor: null,
  loading: false,
  loaded: false,
  searchQuery: '',
  sortBy: 'created-desc',
  viewMode: 'grid',
  lastData: null,
}

function setupFileManager() {
  const grid = document.getElementById('file-grid')
  if (!grid) return

  document.getElementById('manage-refresh-btn')?.addEventListener('click', () => refreshFileManager(true))
  document.getElementById('manage-select-all-btn')?.addEventListener('click', toggleSelectAll)
  document.getElementById('manage-download-btn')?.addEventListener('click', downloadSelected)
  document.getElementById('manage-delete-btn')?.addEventListener('click', deleteSelected)

  // View Mode Switcher
  const gridBtn = document.getElementById('view-grid-btn')
  const listBtn = document.getElementById('view-list-btn')

  function setViewMode(mode) {
    fileManager.viewMode = mode
    gridBtn?.classList.toggle('is-active', mode === 'grid')
    listBtn?.classList.toggle('is-active', mode === 'list')
    if (grid) {
      grid.classList.toggle('view-mode-grid', mode === 'grid')
      grid.classList.toggle('view-mode-list', mode === 'list')
    }
  }

  gridBtn?.addEventListener('click', () => setViewMode('grid'))
  listBtn?.addEventListener('click', () => setViewMode('list'))

  // Floating Selection Bar Action Handlers
  document.getElementById('selection-download-btn')?.addEventListener('click', downloadSelected)
  document.getElementById('selection-delete-btn')?.addEventListener('click', deleteSelected)
  document.getElementById('selection-cancel-btn')?.addEventListener('click', () => {
    document.querySelectorAll('.file-card.is-selected').forEach((c) => c.classList.remove('is-selected'))
    recomputeSelected()
    updateManageToolbar()
  })

  // Search input & clear button
  const searchInput = document.getElementById('manage-search-input')
  const searchClearBtn = document.getElementById('manage-search-clear')
  searchInput?.addEventListener('input', (e) => {
    fileManager.searchQuery = e.target.value.trim()
    if (searchClearBtn) searchClearBtn.hidden = !fileManager.searchQuery
    renderFileManager()
  })
  searchClearBtn?.addEventListener('click', () => {
    if (searchInput) searchInput.value = ''
    fileManager.searchQuery = ''
    searchClearBtn.hidden = true
    renderFileManager()
  })

  // Sort select
  const sortSelect = document.getElementById('manage-sort-select')
  sortSelect?.addEventListener('change', (e) => {
    fileManager.sortBy = e.target.value
    renderFileManager()
  })

  // Setup Delete Files Modal
  const deleteFilesModal = document.getElementById('delete-files-modal')
  const deleteFilesCancelBtn = document.getElementById('delete-files-cancel-btn')
  const deleteFilesConfirmBtn = document.getElementById('delete-files-confirm-btn')

  deleteFilesCancelBtn?.addEventListener('click', hideDeleteFilesModal)
  deleteFilesModal?.addEventListener('click', (e) => {
    if (e.target === deleteFilesModal) hideDeleteFilesModal()
  })
  deleteFilesConfirmBtn?.addEventListener('click', confirmDeleteFilesAction)

  // Keyboard shortcut: Delete or Backspace key to trigger delete on selected items
  window.addEventListener('keydown', (e) => {
    if (e.key === 'Delete' || e.key === 'Backspace') {
      const activeTab = document.querySelector('.tab-btn.is-active')?.dataset?.tab
      if (activeTab === 'manage' && fileManager.selected.size > 0) {
        const tag = document.activeElement?.tagName
        if (tag !== 'INPUT' && tag !== 'TEXTAREA' && tag !== 'SELECT') {
          e.preventDefault()
          deleteSelected()
        }
      }
    }
  })

  setupLassoSelection(grid)
}

async function refreshFileManager(force = false) {
  if (fileManager.loading) return
  if (fileManager.loaded && !force) return
  fileManager.loading = true
  try {
    const res = await fetch('/api/files', { credentials: 'same-origin' })
    if (res.status === 401) {
      // Session expired; let the user re-login from the top of the page.
      window.location.reload()
      return
    }
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    fileManager.files = Array.isArray(data.files) ? data.files : []
    fileManager.lastData = data
    fileManager.loaded = true
    renderFileManager(data)
  } catch (err) {
    console.error('Load file list failed', err)
    showToast('加载文件列表失败，请重试', 'error')
  } finally {
    fileManager.loading = false
  }
}

function getFilteredAndSortedFiles() {
  let list = [...fileManager.files]
  if (fileManager.searchQuery) {
    const q = fileManager.searchQuery.toLowerCase()
    list = list.filter((f) => {
      const name = (f.filename || '').toLowerCase()
      const type = (f.contentType || '').toLowerCase()
      return name.includes(q) || type.includes(q)
    })
  }

  switch (fileManager.sortBy) {
    case 'created-asc':
      list.sort((a, b) => (a.created || 0) - (b.created || 0))
      break
    case 'name-asc':
      list.sort((a, b) => (a.filename || '').localeCompare(b.filename || '', 'zh-CN'))
      break
    case 'name-desc':
      list.sort((a, b) => (b.filename || '').localeCompare(a.filename || '', 'zh-CN'))
      break
    case 'size-desc':
      list.sort((a, b) => (b.size || 0) - (a.size || 0))
      break
    case 'size-asc':
      list.sort((a, b) => (a.size || 0) - (b.size || 0))
      break
    case 'expires-asc':
      list.sort((a, b) => (a.expires || Infinity) - (b.expires || Infinity))
      break
    case 'created-desc':
    default:
      list.sort((a, b) => (b.created || 0) - (a.created || 0))
      break
  }
  return list
}

function getFileTypeTheme(filename = '', mimeType = '') {
  const ext = filename.split('.').pop()?.toLowerCase() || ''
  if (/image|png|jpg|jpeg|gif|webp|svg/i.test(mimeType) || ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(ext)) {
    return {
      category: 'image',
      icon: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2.5" ry="2.5"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/></svg>`,
    }
  }
  if (/video|mp4|webm|mkv|avi/i.test(mimeType) || ['mp4', 'webm', 'mkv', 'mov', 'avi'].includes(ext)) {
    return {
      category: 'video',
      icon: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="23 7 16 12 23 17 23 7"/><rect x="1" y="5" width="15" height="14" rx="2" ry="2"/></svg>`,
    }
  }
  if (/audio|mp3|wav|flac|ogg/i.test(mimeType) || ['mp3', 'wav', 'flac', 'm4a', 'aac'].includes(ext)) {
    return {
      category: 'audio',
      icon: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>`,
    }
  }
  if (/pdf|document|word|text|md|txt/i.test(mimeType) || ['pdf', 'doc', 'docx', 'txt', 'md', 'rtf'].includes(ext)) {
    return {
      category: 'document',
      icon: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg>`,
    }
  }
  if (['zip', 'rar', '7z', 'tar', 'gz'].includes(ext)) {
    return {
      category: 'archive',
      icon: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 8v13H3V3h10l8 5z"/><path d="M10 12h4"/><path d="M10 16h4"/></svg>`,
    }
  }
  if (['js', 'ts', 'go', 'py', 'java', 'c', 'cpp', 'html', 'css', 'json', 'sh', 'sql'].includes(ext)) {
    return {
      category: 'code',
      icon: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>`,
    }
  }
  return {
    category: 'default',
    icon: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>`,
  }
}

function renderFileManager(data = fileManager.lastData) {
  if (data) fileManager.lastData = data
  const grid = document.getElementById('file-grid')
  const empty = document.getElementById('manage-empty')
  const emptyText = document.getElementById('manage-empty-text')
  if (!grid) return

  // Reset selection state for the new list.
  fileManager.selected.clear()
  fileManager.lastAnchor = null

  grid.querySelectorAll('.file-card').forEach((el) => el.remove())

  const summary = document.getElementById('manage-summary')
  const usage = document.getElementById('pill-storage-usage')
  const retention = document.getElementById('pill-retention-mg')
  const countBadge = document.getElementById('manage-count')

  if (!fileManager.files.length) {
    if (empty) {
      empty.hidden = false
      empty.style.display = 'flex'
    }
    if (emptyText) emptyText.textContent = '服务器上暂无文件，点击上传文件或刷新'
    if (summary) summary.textContent = '服务器上暂无文件'
    if (usage) usage.innerHTML = '已用 <strong>0 B</strong>'
    if (countBadge) countBadge.hidden = true
    updateManageToolbar()
    return
  }

  const displayedFiles = getFilteredAndSortedFiles()

  if (!displayedFiles.length) {
    if (empty) {
      empty.hidden = false
      empty.style.display = 'flex'
    }
    if (emptyText) emptyText.textContent = `未找到与 「${fileManager.searchQuery}」 匹配的文件`
  } else {
    if (empty) {
      empty.hidden = true
      empty.style.display = 'none'
    }
    for (const file of displayedFiles) {
      grid.appendChild(renderFileCard(file))
    }
  }

  const totalBytes = fileManager.lastData?.totalBytes || 0
  if (summary) {
    if (fileManager.searchQuery) {
      summary.textContent = `匹配 ${displayedFiles.length} / 共 ${fileManager.files.length} 个文件`
    } else {
      summary.textContent = `共 ${fileManager.files.length} 个文件`
    }
  }
  if (usage) usage.innerHTML = `已用 <strong>${formatBytes(totalBytes)}</strong>`
  if (retention) retention.innerHTML = `保留 <strong>${fileManager.lastData?.retention || '-'}</strong>`
  if (countBadge) {
    countBadge.hidden = false
    countBadge.textContent = fileManager.files.length
  }
  updateManageToolbar()
}

function renderFileCard(file) {
  const card = document.createElement('article')
  card.className = 'file-card'
  card.setAttribute('role', 'listitem')
  card.dataset.id = file.id
  card.tabIndex = 0

  const theme = getFileTypeTheme(file.filename, file.contentType)
  card.dataset.category = theme.category

  const isImage = theme.category === 'image'

  let icon
  if (isImage && file.downloadUrl) {
    icon = document.createElement('div')
    icon.className = 'file-card-thumb-wrap'
    const img = document.createElement('img')
    img.className = 'file-card-thumb'
    img.src = file.downloadUrl
    img.alt = file.filename || ''
    img.loading = 'lazy'
    img.onerror = () => {
      icon.className = 'file-card-icon'
      icon.innerHTML = theme.icon
    }
    icon.appendChild(img)
  } else {
    icon = document.createElement('div')
    icon.className = 'file-card-icon'
    icon.innerHTML = theme.icon
  }

  const body = document.createElement('div')
  body.className = 'file-card-body'

  const name = document.createElement('span')
  name.className = 'file-card-name'
  name.textContent = file.filename || '未命名文件'
  name.title = file.filename || ''

  const expiryInfo = formatExpiryInfo(file)

  const meta = document.createElement('span')
  meta.className = 'file-card-meta'
  meta.innerHTML = `${formatBytes(file.size)}${expiryInfo.text ? ` · <span class="expiry-badge ${expiryInfo.level}">${expiryInfo.text}</span>` : ''}`

  body.append(name, meta)

  const actions = document.createElement('div')
  actions.className = 'file-card-actions'

  const dlBtn = document.createElement('button')
  dlBtn.type = 'button'
  dlBtn.className = 'btn-icon-action'
  dlBtn.innerHTML = svgIcon('download')
  dlBtn.title = '下载文件'
  dlBtn.addEventListener('click', (e) => {
    e.stopPropagation()
    if (file.downloadUrl) triggerDownload(file.downloadUrl)
  })

  const copyBtn = document.createElement('button')
  copyBtn.type = 'button'
  copyBtn.className = 'btn-icon-action'
  copyBtn.innerHTML = svgIcon('copy')
  copyBtn.title = '复制下载链接'
  copyBtn.addEventListener('click', (e) => {
    e.stopPropagation()
    if (file.downloadUrl) {
      copyToClipboard(file.downloadUrl)
      showToast('已复制文件下载链接', 'success')
    }
  })

  const delBtn = document.createElement('button')
  delBtn.type = 'button'
  delBtn.className = 'btn-icon-action btn-danger-action'
  delBtn.innerHTML = svgIcon('trash')
  delBtn.title = '从服务器删除'
  delBtn.addEventListener('click', (e) => {
    e.stopPropagation()
    openDeleteFilesModal([file.id])
  })

  actions.append(dlBtn, copyBtn, delBtn)

  // Selection checkbox
  const check = document.createElement('span')
  check.className = 'file-card-check'
  check.innerHTML = svgIcon('check')

  card.append(check, icon, body, actions)

  // Mobile Touch & Long-Press (长按 450ms) gesture support
  let longPressTimer = null
  let touchStartX = 0
  let touchStartY = 0
  let isLongPress = false

  card.addEventListener('touchstart', (e) => {
    if (e.target.closest('button')) return
    touchStartX = e.touches[0].clientX
    touchStartY = e.touches[0].clientY
    isLongPress = false

    longPressTimer = setTimeout(() => {
      isLongPress = true
      if (navigator.vibrate) navigator.vibrate(40)
      card.classList.add('is-selected')
      fileManager.lastAnchor = file.id
      recomputeSelected()
      updateManageToolbar()
      showToast(`已选中文件 「${file.filename || ''}」`, 'info')
    }, 450)
  }, { passive: true })

  card.addEventListener('touchmove', (e) => {
    if (!longPressTimer) return
    const dx = Math.abs(e.touches[0].clientX - touchStartX)
    const dy = Math.abs(e.touches[0].clientY - touchStartY)
    if (dx > 8 || dy > 8) {
      clearTimeout(longPressTimer)
      longPressTimer = null
    }
  }, { passive: true })

  card.addEventListener('touchend', () => {
    if (longPressTimer) {
      clearTimeout(longPressTimer)
      longPressTimer = null
    }
  }, { passive: true })

  card.addEventListener('touchcancel', () => {
    if (longPressTimer) {
      clearTimeout(longPressTimer)
      longPressTimer = null
    }
  }, { passive: true })

  card.addEventListener('click', (e) => {
    if (e.target.closest('button')) return
    if (isLongPress) {
      e.stopPropagation()
      isLongPress = false
      return
    }
    toggleSelect(card, e.shiftKey, e.ctrlKey || e.metaKey)
  })
  card.addEventListener('keydown', (e) => {
    if (e.key === ' ' || e.key === 'Enter') {
      e.preventDefault()
      toggleSelect(card, e.shiftKey, e.ctrlKey || e.metaKey)
    }
  })

  return card
}

function formatExpiryInfo(file) {
  if (!file.expires) return { text: '', level: 'normal' }
  const diff = file.expires - Math.floor(Date.now() / 1000)
  if (diff <= 0) return { text: '已过期', level: 'expired' }
  if (diff < 3600) {
    const m = Math.max(1, Math.round(diff / 60))
    return { text: `${m} 分钟后过期`, level: 'warning' }
  }
  if (diff < 86400) {
    const h = Math.round(diff / 3600)
    return { text: `${h} 小时后过期`, level: 'normal' }
  }
  const d = Math.round(diff / 86400)
  return { text: `${d} 天后过期`, level: 'normal' }
}

function toggleSelect(card, shift, ctrl) {
  const id = card.dataset.id
  if (shift && fileManager.lastAnchor) {
    const cards = [...document.querySelectorAll('.file-card')]
    const a = cards.findIndex((c) => c.dataset.id === fileManager.lastAnchor)
    const b = cards.findIndex((c) => c.dataset.id === id)
    if (a >= 0 && b >= 0) {
      const [from, to] = a < b ? [a, b] : [b, a]
      for (let i = from; i <= to; i++) cards[i].classList.add('is-selected')
      recomputeSelected()
      updateManageToolbar()
      return
    }
  }
  if (ctrl) {
    card.classList.toggle('is-selected')
  } else if (card.classList.contains('is-selected') && fileManager.selected.size === 1) {
    card.classList.remove('is-selected')
  } else {
    // Single click without modifier: select only this one.
    document.querySelectorAll('.file-card.is-selected').forEach((c) => c.classList.remove('is-selected'))
    card.classList.add('is-selected')
  }
  if (card.classList.contains('is-selected')) fileManager.lastAnchor = id
  recomputeSelected()
  updateManageToolbar()
}

function recomputeSelected() {
  fileManager.selected.clear()
  document.querySelectorAll('.file-card.is-selected').forEach((c) => fileManager.selected.add(c.dataset.id))
}

function toggleSelectAll() {
  const cards = document.querySelectorAll('.file-card')
  if (!cards.length) return
  const allSelected = [...cards].every((c) => c.classList.contains('is-selected'))
  cards.forEach((c) => c.classList.toggle('is-selected', !allSelected))
  recomputeSelected()
  updateManageToolbar()
}

function updateManageToolbar() {
  const n = fileManager.selected.size
  const dlBtn = document.getElementById('manage-download-btn')
  const delBtn = document.getElementById('manage-delete-btn')
  const selectAll = document.getElementById('manage-select-all-btn')
  if (dlBtn) dlBtn.disabled = n === 0
  if (delBtn) {
    delBtn.disabled = n === 0
    delBtn.textContent = n > 0 ? `批量删除 (${n})` : '批量删除'
  }
  const total = fileManager.files.length
  if (selectAll) selectAll.textContent = total > 0 && n === total ? '取消全选' : '全选'

  // Update floating selection action bar for mobile & touch!
  const floatBar = document.getElementById('selection-action-bar')
  const floatInfo = document.getElementById('selection-action-info')
  if (floatBar) {
    if (n > 0) {
      floatBar.hidden = false
      floatBar.setAttribute('aria-hidden', 'false')
      if (floatInfo) floatInfo.textContent = `已选中 ${n} 个文件`
    } else {
      floatBar.hidden = true
      floatBar.setAttribute('aria-hidden', 'true')
    }
  }
}

function downloadSelected() {
  const ids = [...fileManager.selected]
  if (!ids.length) return
  const url = `/api/files/archive?ids=${ids.map(encodeURIComponent).join(',')}`
  triggerDownload(url)
  showToast(`正在打包下载 ${ids.length} 个文件…`, 'info')
}

function triggerDownload(url) {
  const a = document.createElement('a')
  a.href = url
  a.download = ''
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  a.remove()
}

function deleteSelected() {
  const ids = [...fileManager.selected]
  if (!ids.length) return
  openDeleteFilesModal(ids)
}

function openDeleteFilesModal(ids) {
  activeDeleteFileIds = ids
  const modal = document.getElementById('delete-files-modal')
  const title = document.getElementById('delete-files-title')
  const desc = document.getElementById('delete-files-desc')
  const preview = document.getElementById('delete-files-preview')
  const confirmBtn = document.getElementById('delete-files-confirm-btn')

  if (!modal || !preview || !confirmBtn) return

  confirmBtn.disabled = false
  confirmBtn.textContent = '确认删除'

  const targets = fileManager.files.filter((f) => ids.includes(f.id))
  const count = targets.length

  if (title) title.textContent = count === 1 ? '确认删除文件？' : `确认批量删除 ${count} 个文件？`
  if (desc) desc.textContent = count === 1
    ? `您即将从服务器彻底删除「${targets[0]?.filename || '此文件'}」，删除后无法恢复。`
    : `您即将从服务器彻底删除选中的 ${count} 个文件，此操作不可撤销。`

  // Build preview list HTML
  preview.innerHTML = ''
  const showItems = targets.slice(0, 5)
  showItems.forEach((f) => {
    const item = document.createElement('div')
    item.className = 'delete-preview-item'
    item.innerHTML = `
      ${getFileTypeIcon(f.filename, f.contentType)}
      <span class="delete-preview-name">${f.filename || '未命名文件'}</span>
      <span class="delete-preview-size">${formatBytes(f.size)}</span>
    `
    preview.appendChild(item)
  })

  if (targets.length > 5) {
    const more = document.createElement('div')
    more.className = 'delete-preview-more'
    more.textContent = `等共 ${targets.length} 个文件`
    preview.appendChild(more)
  }

  modal.classList.add('is-active')
  modal.setAttribute('aria-hidden', 'false')
}

function hideDeleteFilesModal() {
  const modal = document.getElementById('delete-files-modal')
  if (!modal) return
  modal.classList.remove('is-active')
  modal.setAttribute('aria-hidden', 'true')
  activeDeleteFileIds = []
}

async function confirmDeleteFilesAction() {
  if (!activeDeleteFileIds.length) return
  const confirmBtn = document.getElementById('delete-files-confirm-btn')
  if (confirmBtn) {
    confirmBtn.disabled = true
    confirmBtn.textContent = '正在删除…'
  }
  await performDeleteFiles(activeDeleteFileIds)
  hideDeleteFilesModal()
}

async function performDeleteFiles(ids) {
  try {
    const res = await fetch('/api/files/delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ ids }),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      throw new Error(body.error || `HTTP ${res.status}`)
    }
    const result = await res.json()
    const removed = result.removed || 0
    showToast(`已成功删除 ${removed} 个文件`, 'success')
    // Reload the list from the server to stay in sync.
    fileManager.loaded = false
    refreshFileManager(true)
  } catch (err) {
    console.error('Batch delete failed', err)
    showToast(`删除失败：${err.message}`, 'error')
  }
}

// ---- Lasso (rubber-band) selection -----------------------------------------

function setupLassoSelection(grid) {
  const box = document.getElementById('lasso-box')
  const container = grid.parentElement
  if (!box || !container) return

  let dragging = false
  let startX = 0
  let startY = 0
  let baseSelection = new Set()

  const pointFrom = (e) => {
    const rect = container.getBoundingClientRect()
    const cx = e.touches ? e.touches[0].clientX : e.clientX
    const cy = e.touches ? e.touches[0].clientY : e.clientY
    return { x: cx - rect.left, y: cy - rect.top }
  }

  const onDown = (e) => {
    // Only start a lasso on empty grid space (not on a card/button).
    if (e.target.closest('.file-card')) return
    if (e.button !== undefined && e.button !== 0) return
    const p = pointFrom(e)
    dragging = true
    startX = p.x
    startY = p.y
    // Snapshot the current selection so shift/ctrl-free lasso toggles within it.
    baseSelection = new Set(fileManager.selected)
    box.hidden = false
    box.style.left = `${p.x}px`
    box.style.top = `${p.y}px`
    box.style.width = '0px'
    box.style.height = '0px'
    if (e.cancelable) e.preventDefault()
  }

  const onMove = (e) => {
    if (!dragging) return
    const p = pointFrom(e)
    const x = Math.min(p.x, startX)
    const y = Math.min(p.y, startY)
    const w = Math.abs(p.x - startX)
    const h = Math.abs(p.y - startY)
    box.style.left = `${x}px`
    box.style.top = `${y}px`
    box.style.width = `${w}px`
    box.style.height = `${h}px`
    selectIntersecting(x, y, w, h)
    if (e.cancelable) e.preventDefault()
  }

  const onUp = () => {
    if (!dragging) return
    dragging = false
    box.hidden = true
    recomputeSelected()
    updateManageToolbar()
  }

  const selectIntersecting = (x, y, w, h) => {
    const containerRect = container.getBoundingClientRect()
    document.querySelectorAll('.file-card').forEach((card) => {
      const r = card.getBoundingClientRect()
      const cx = r.left - containerRect.left
      const cy = r.top - containerRect.top
      const cw = r.width
      const ch = r.height
      const intersects = !(cx > x + w || cx + cw < x || cy > y + h || cy + ch < y)
      const id = card.dataset.id
      if (intersects) {
        card.classList.add('is-selected')
      } else if (!baseSelection.has(id)) {
        card.classList.remove('is-selected')
      }
    })
  }

  container.addEventListener('mousedown', onDown)
  window.addEventListener('mousemove', onMove)
  window.addEventListener('mouseup', onUp)
  container.addEventListener('touchstart', onDown, { passive: false })
  window.addEventListener('touchmove', onMove, { passive: false })
  window.addEventListener('touchend', onUp)
}

function zhLocale() {
  return {
    strings: {
      browse: '选择文件',
      browseFiles: '浏览文件',
      dropPasteFiles: '拖拽文件到这里，或 %{browse}',
      dropHint: '拖放文件到这里上传',
      addMore: '添加更多',
      addMoreFiles: '添加更多文件',
      upload: '开始上传',
      uploadXFiles: '开始上传 %{smart_count} 个文件',
      uploadXNewFiles: '上传 %{smart_count} 个新文件',
      pauseUpload: '暂停上传',
      resumeUpload: '继续上传',
      retryUpload: '重试',
      retry: '重试',
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
