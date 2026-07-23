import { rm } from 'node:fs/promises'
import { build } from 'esbuild'

const outdir = 'src/web/vendor/uppy'

await rm(outdir, { recursive: true, force: true })

await build({
  entryPoints: [
    'src/web/vendor-src/uppy/core.js',
    'src/web/vendor-src/uppy/webcam.js',
  ],
  bundle: true,
  chunkNames: 'chunks/[name]-[hash]',
  entryNames: '[name]',
  format: 'esm',
  minify: true,
  outdir,
  platform: 'browser',
  splitting: true,
  target: 'es2020',
})

await build({
  entryPoints: [
    'src/web/vendor-src/uppy/core.css',
    'src/web/vendor-src/uppy/webcam.css',
  ],
  bundle: true,
  entryNames: '[name]',
  minify: true,
  outdir,
})
