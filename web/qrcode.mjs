// qrcode.mjs
// Lightweight, zero-dependency QR Code SVG Generator for Browser ES Modules.
// Generates responsive SVG elements for share URLs.

export function generateQRCodeSVG(text, options = {}) {
  const size = options.size || 220
  const color = options.color || '#1e1b2e'
  const bgColor = options.bgColor || '#ffffff'
  const margin = options.margin !== undefined ? options.margin : 2

  const qr = createQRCode(text)
  const modules = qr.modules
  const count = modules.length

  let pathData = ''
  for (let r = 0; r < count; r++) {
    for (let c = 0; c < count; c++) {
      if (modules[r][c]) {
        pathData += `M${c + margin},${r + margin}h1v1h-1z `
      }
    }
  }

  const viewBoxSize = count + margin * 2
  return `
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${viewBoxSize} ${viewBoxSize}" width="${size}" height="${size}" style="display:block;margin:0 auto;border-radius:12px;background:${bgColor};padding:12px;box-shadow:0 10px 30px rgba(0,0,0,0.08)">
      <path d="${pathData.trim()}" fill="${color}" shape-rendering="crispEdges"/>
    </svg>
  `
}

// Minimal Reed-Solomon & QR Code Matrix generator supporting URLs up to 300+ chars
function createQRCode(text) {
  const data = new TextEncoder().encode(text)
  let version = 1
  const capacity = [17, 32, 53, 78, 106, 134, 154, 192, 230, 271, 321, 367, 425]
  while (version < capacity.length && data.length > capacity[version - 1]) {
    version++
  }
  if (version > capacity.length) {
    version = capacity.length
  }

  const moduleCount = 17 + 4 * version
  const modules = Array.from({ length: moduleCount }, () => Array(moduleCount).fill(false))
  const isFunction = Array.from({ length: moduleCount }, () => Array(moduleCount).fill(false))

  function setModule(r, c, val) {
    modules[r][c] = val
    isFunction[r][c] = true
  }

  // Finder patterns
  function placeFinder(r, c) {
    for (let dy = -1; dy <= 7; dy++) {
      for (let dx = -1; dx <= 7; dx++) {
        const row = r + dy
        const col = c + dx
        if (row >= 0 && row < moduleCount && col >= 0 && col < moduleCount) {
          const isDark =
            dy >= 0 && dy <= 6 && dx >= 0 && dx <= 6 &&
            (dy === 0 || dy === 6 || dx === 0 || dx === 6 || (dy >= 2 && dy <= 4 && dx >= 2 && dx <= 4))
          setModule(row, col, isDark)
        }
      }
    }
  }
  placeFinder(0, 0)
  placeFinder(moduleCount - 7, 0)
  placeFinder(0, moduleCount - 7)

  // Timing patterns
  for (let i = 8; i < moduleCount - 8; i++) {
    if (!isFunction[6][i]) setModule(6, i, i % 2 === 0)
    if (!isFunction[i][6]) setModule(i, 6, i % 2 === 0)
  }

  // Alignment pattern (version >= 2)
  if (version >= 2) {
    const alignPos = getAlignmentPositions(version)
    for (const r of alignPos) {
      for (const c of alignPos) {
        if (isFunction[r][c]) continue
        for (let dy = -2; dy <= 2; dy++) {
          for (let dx = -2; dx <= 2; dx++) {
            const isDark = Math.max(Math.abs(dy), Math.abs(dx)) !== 1
            setModule(r + dy, c + dx, isDark)
          }
        }
      }
    }
  }

  // Dark module
  setModule(4 * version + 9, 8, true)

  // Format & Version reserve
  for (let i = 0; i < 9; i++) {
    if (!isFunction[8][i]) setModule(8, i, false)
    if (!isFunction[i][8]) setModule(i, 8, false)
  }
  for (let i = 0; i < 8; i++) {
    if (!isFunction[8][moduleCount - 1 - i]) setModule(8, moduleCount - 1 - i, false)
    if (!isFunction[moduleCount - 1 - i][8]) setModule(moduleCount - 1 - i, 8, false)
  }

  // Encode Data
  const bits = []
  function pushBits(val, len) {
    for (let i = len - 1; i >= 0; i--) {
      bits.push((val >> i) & 1)
    }
  }
  // Mode: Byte (0100)
  pushBits(0b0100, 4)
  const countBits = version <= 9 ? 8 : 16
  pushBits(data.length, countBits)
  for (const byte of data) {
    pushBits(byte, 8)
  }

  // Terminate & Pad
  const totalDataBytes = getNumDataBytes(version)
  const totalDataBits = totalDataBytes * 8
  if (bits.length + 4 <= totalDataBits) pushBits(0, 4)
  while (bits.length % 8 !== 0) bits.push(0)
  const padBytes = [0xec, 0x11]
  let padIdx = 0
  while (bits.length < totalDataBits) {
    pushBits(padBytes[padIdx % 2], 8)
    padIdx++
  }

  const rawBytes = []
  for (let i = 0; i < bits.length; i += 8) {
    let b = 0
    for (let j = 0; j < 8; j++) b = (b << 1) | bits[i + j]
    rawBytes.push(b)
  }

  // Add Error Correction (RS)
  const ecBytesPerBlock = getECBytes(version)
  const ec = calculateReedSolomon(rawBytes, ecBytesPerBlock)
  const finalBytes = [...rawBytes, ...ec]

  // Interleave and place bits
  const finalBits = []
  for (const byte of finalBytes) {
    for (let i = 7; i >= 0; i--) finalBits.push((byte >> i) & 1)
  }

  let bitIdx = 0
  let dir = -1
  let x = moduleCount - 1
  let y = moduleCount - 1

  while (x > 0) {
    if (x === 6) x-- // Skip vertical timing column
    for (let i = 0; i < 2; i++) {
      const currX = x - i
      if (!isFunction[y][currX]) {
        let val = false
        if (bitIdx < finalBits.length) {
          val = finalBits[bitIdx++] === 1
        }
        // Simple mask 0: (row + col) % 2 === 0
        const mask = (y + currX) % 2 === 0
        modules[y][currX] = val ^ mask
      }
    }
    y += dir
    if (y < 0 || y >= moduleCount) {
      dir = -dir
      y += dir
      x -= 2
    }
  }

  // Draw Format Information (Mask 0, Level L)
  const formatInfo = 0x77c4 // Mask 0, L
  for (let i = 0; i < 15; i++) {
    const val = ((formatInfo >> i) & 1) === 1
    if (i < 6) modules[8][i] = val
    else if (i < 8) modules[8][i + 1] = val
    else if (i === 8) modules[8 + 1][8] = val
    else modules[moduleCount - 15 + i][8] = val

    if (i < 8) modules[moduleCount - 1 - i][8] = val
    else modules[8][moduleCount - 15 + i] = val
  }

  return { modules }
}

function getAlignmentPositions(version) {
  if (version === 1) return []
  const pos = [6]
  const last = 17 + 4 * version - 7
  const count = Math.floor(version / 7) + 2
  const step = Math.ceil((last - 6) / (count - 1))
  for (let i = count - 1; i > 0; i--) pos.splice(1, 0, last - (count - 1 - i) * step)
  return pos
}

function getNumDataBytes(version) {
  const table = [19, 34, 55, 80, 108, 136, 156, 194, 232, 274, 324, 370, 428]
  return table[version - 1] || table[table.length - 1]
}

function getECBytes(version) {
  const table = [7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26]
  return table[version - 1] || 10
}

function calculateReedSolomon(data, ecCount) {
  const gfExp = new Uint8Array(512)
  const gfLog = new Uint8Array(256)
  let x = 1
  for (let i = 0; i < 255; i++) {
    gfExp[i] = x
    gfExp[i + 255] = x
    gfLog[x] = i
    x = (x << 1) ^ (x & 0x80 ? 0x11d : 0)
  }

  function gfMul(x, y) {
    if (x === 0 || y === 0) return 0
    return gfExp[gfLog[x] + gfLog[y]]
  }

  // Generator polynomial
  let poly = [1]
  for (let i = 0; i < ecCount; i++) {
    const nextPoly = new Array(poly.length + 1).fill(0)
    for (let j = 0; j < poly.length; j++) {
      nextPoly[j] ^= poly[j]
      nextPoly[j + 1] ^= gfMul(poly[j], gfExp[i])
    }
    poly = nextPoly
  }

  const res = new Uint8Array(ecCount)
  for (const byte of data) {
    const factor = byte ^ res[0]
    for (let i = 0; i < ecCount - 1; i++) {
      res[i] = res[i + 1] ^ gfMul(poly[i + 1], factor)
    }
    res[ecCount - 1] = gfMul(poly[ecCount], factor)
  }
  return Array.from(res)
}
