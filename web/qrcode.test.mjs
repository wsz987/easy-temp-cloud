import test from 'node:test'
import assert from 'node:assert/strict'

import { generateQRCodeSVG } from './qrcode.mjs'

test('renders a standard four-module quiet zone around a short URL', () => {
  const svg = generateQRCodeSVG('https://example.com', { size: 200 })

  assert.match(svg, /viewBox="0 0 33 33"/)
  assert.match(svg, /<path d="M/)
})

test('renders a QR matrix for a realistic file download URL', () => {
  const url = `https://files.example.com/files/${'a'.repeat(64)}?key=${'b'.repeat(64)}`
  const svg = generateQRCodeSVG(url, { size: 200 })

  assert.match(svg, /<svg /)
  assert.match(svg, /<path d="M/)
})
