import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const source = await readFile(new URL('./app.js', import.meta.url), 'utf8')

test('stores a bearer token locally and attaches it to protected browser requests', () => {
  assert.match(source, /localStorage\.getItem\(AUTH_TOKEN_STORAGE_KEY\)/)
  assert.match(source, /Authorization: `Bearer \$\{token\}`/)
  assert.match(source, /headers: authHeaders\(\)/)
})

test('does not expose AUTH_PASSWORD in the browser client', () => {
  assert.doesNotMatch(source, /apiPassword/)
  assert.doesNotMatch(source, /\?pwd=/)
})

test('loads the optional webcam feature from its own Uppy bundle', () => {
  assert.match(source, /from '\.\/vendor\/uppy\/core\.js'/)
  assert.match(source, /import\('\.\/vendor\/uppy\/webcam\.js'\)/)
  assert.doesNotMatch(source, /uppy\.min\.mjs/)
})
