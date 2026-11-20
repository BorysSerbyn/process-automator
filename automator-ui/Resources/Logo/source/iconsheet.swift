// usage: iconsheet <iconset dir> <out.png> <light|dark>
import AppKit

let dir = CommandLine.arguments[1], outPath = CommandLine.arguments[2]
let isDark = CommandLine.arguments[3] == "dark"
func load(_ n: String) -> NSBitmapImageRep {
    NSBitmapImageRep(data: try! Data(contentsOf: URL(fileURLWithPath: "\(dir)/\(n).png")))!
}
let W = 1260, H = 600
let sheet = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: W, pixelsHigh: H, bitsPerSample: 8, samplesPerPixel: 4,
    hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: sheet)
(isDark ? NSColor(white: 0.12, alpha: 1) : NSColor(white: 0.93, alpha: 1)).setFill()
NSRect(x: 0, y: 0, width: W, height: H).fill()
let fg = isDark ? NSColor(white: 0.8, alpha: 1) : NSColor(white: 0.3, alpha: 1)
func put(_ r: NSBitmapImageRep, _ x: Int, _ yTop: Int, _ zoom: Int = 1, _ label: String? = nil) {
    let w = r.pixelsWide * zoom, h = r.pixelsHigh * zoom
    r.draw(in: NSRect(x: x, y: H - yTop - h, width: w, height: h), from: .zero, operation: .sourceOver, fraction: 1,
        respectFlipped: false, hints: [.interpolation: NSImageInterpolation.none.rawValue])
    if let label {
        (label as NSString).draw(at: NSPoint(x: x, y: H - yTop - h - 18), withAttributes: [.foregroundColor: fg, .font: NSFont.systemFont(ofSize: 11)])
    }
}
put(load("icon_256x256@2x"), 20, 20, 1, "512 px")
put(load("icon_128x128@2x"), 550, 20, 1, "256 px")
put(load("icon_128x128"), 825, 20, 1, "128 px")
put(load("icon_32x32@2x"), 970, 20, 1, "64 px")
put(load("icon_32x32"), 1050, 20, 1, "32")
put(load("icon_16x16"), 1100, 20, 1, "16")
put(load("icon_32x32"), 825, 300, 4, "32 px, zoomed 4x")
put(load("icon_16x16"), 1000, 300, 8, "16 px, zoomed 8x")
NSGraphicsContext.restoreGraphicsState()
try! sheet.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: outPath))
