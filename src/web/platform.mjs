export function detectMobilePlatform(navigatorLike = globalThis.navigator ?? {}) {
  const userAgent = navigatorLike.userAgent || ''
  const platform = navigatorLike.platform || ''
  const maxTouchPoints = navigatorLike.maxTouchPoints || 0

  const isIPadOS = platform === 'MacIntel' && maxTouchPoints > 1
  if (/iPhone|iPad|iPod/i.test(userAgent) || isIPadOS) return 'ios'
  if (/Android/i.test(userAgent)) return 'android'
  if (/Mobi|Mobile|Phone|Tablet/i.test(userAgent)) return 'mobile'
  return 'desktop'
}

export function isMobilePlatform(navigatorLike = globalThis.navigator ?? {}) {
  return detectMobilePlatform(navigatorLike) !== 'desktop'
}

export function uploadNoteForPlatform(platform) {
  if (platform === 'desktop') return '支持拖拽或点击选择上传'
  return '支持点击选择或拍照/录像上传'
}
