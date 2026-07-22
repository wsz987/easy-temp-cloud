import qrcode from './vendor/qrcode-generator.mjs'

export function generateQRCodeSVG(text, options = {}) {
  const size = options.size || 220
  const color = options.color || '#1e1b2e'
  const bgColor = options.bgColor || '#ffffff'
  const margin = options.margin !== undefined ? options.margin : 4
  const qr = qrcode(0, 'M')

  qr.addData(String(text), 'Byte')
  qr.make()

  const count = qr.getModuleCount()
  let pathData = ''

  for (let row = 0; row < count; row++) {
    for (let column = 0; column < count; column++) {
      if (qr.isDark(row, column)) {
        pathData += `M${column + margin},${row + margin}h1v1h-1z `
      }
    }
  }

  const viewBoxSize = count + margin * 2
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${viewBoxSize} ${viewBoxSize}" width="${size}" height="${size}" style="display:block;margin:0 auto;background:${bgColor}"><path d="${pathData.trim()}" fill="${color}" shape-rendering="crispEdges"/></svg>`
}
