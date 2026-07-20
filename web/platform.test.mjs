import test from 'node:test'
import assert from 'node:assert/strict'

import {
  detectMobilePlatform,
  isMobilePlatform,
} from './platform.mjs'

test('detects Android browsers as android mobile', () => {
  const navigatorLike = {
    userAgent: 'Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 Chrome/126.0.0.0 Mobile Safari/537.36',
    platform: 'Linux armv81',
    maxTouchPoints: 5,
  }

  assert.equal(detectMobilePlatform(navigatorLike), 'android')
  assert.equal(isMobilePlatform(navigatorLike), true)
})

test('detects iPhone browsers as ios mobile', () => {
  const navigatorLike = {
    userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 Version/18.5 Mobile/15E148 Safari/604.1',
    platform: 'iPhone',
    maxTouchPoints: 5,
  }

  assert.equal(detectMobilePlatform(navigatorLike), 'ios')
  assert.equal(isMobilePlatform(navigatorLike), true)
})

test('detects iPadOS desktop-style Safari as ios mobile', () => {
  const navigatorLike = {
    userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15) AppleWebKit/605.1.15 Version/18.5 Mobile/15E148 Safari/604.1',
    platform: 'MacIntel',
    maxTouchPoints: 5,
  }

  assert.equal(detectMobilePlatform(navigatorLike), 'ios')
  assert.equal(isMobilePlatform(navigatorLike), true)
})

test('does not treat touch-enabled Windows laptops as mobile', () => {
  const navigatorLike = {
    userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0.0.0 Safari/537.36',
    platform: 'Win32',
    maxTouchPoints: 10,
  }

  assert.equal(detectMobilePlatform(navigatorLike), 'desktop')
  assert.equal(isMobilePlatform(navigatorLike), false)
})
