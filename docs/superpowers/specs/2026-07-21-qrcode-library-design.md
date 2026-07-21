# QR Code Library Replacement

## Goal

Replace the hand-written QR encoder used by the mobile sharing dialog with a
maintained QR code library, while preserving the current offline, static
frontend deployment model.

## Scope

- Vendor a browser-compatible, mature QR code generator under `web/vendor/`.
- Keep `generateQRCodeSVG(text, options)` as the application-facing API so
  `web/app.js` and the modal markup do not need to change.
- Render SVG with an explicit quiet zone and high error correction suitable for
  file-sharing URLs.
- Add automated tests that exercise short and realistic long URLs.

## Design

Use the ESM distribution of a mature QR library as a checked-in static asset.
`web/qrcode.mjs` becomes a small adapter: it passes the URL and rendering
options to the library and returns the SVG markup expected by the existing
modal. The adapter owns only presentation defaults such as size, colors, and
quiet-zone margin; QR encoding, capacity selection, masking, error correction,
and block interleaving are delegated to the library.

The browser continues to load only local files. No CDN, runtime download, or
build step is introduced.

## Error Handling

When a URL exceeds the library's supported capacity, the adapter throws a
clear error rather than emitting a malformed code. The caller will preserve
the existing modal behavior for valid upload URLs.

## Verification

- A test must fail against the current custom implementation before the
  library-backed adapter is introduced.
- Tests verify that short and realistic long HTTPS URLs produce SVG output
  with a QR matrix and a quiet zone.
- Run the focused QR tests and the existing JavaScript tests after the change.
