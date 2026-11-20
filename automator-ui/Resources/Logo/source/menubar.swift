// Menu bar previews: glyph.svg + dot.svg drawn like the app draws them (template glyph, tinted dot), next to
// system symbols, at 1x and 2x, for each status colour. Plus nearest-neighbour zooms of the icon alone.
// usage: menubar <glyph.svg> <dot.svg> <out.png> <light|dark>
import AppKit

let a = CommandLine.arguments
let glyph = NSImage(contentsOf: URL(fileURLWithPath: a[1]))!, dot = NSImage(contentsOf: URL(fileURLWithPath: a[2]))!
let isDark = a[4] == "dark"
let bar = isDark ? NSColor(srgbRed: 0.16, green: 0.16, blue: 0.18, alpha: 1) : NSColor(srgbRed: 0.93, green: 0.93, blue: 0.94, alpha: 1)
let fg = isDark ? NSColor.white : NSColor(white: 0, alpha: 0.85)
let dots: [(String, NSColor)] = isDark
    ? [("green", NSColor(srgbRed: 0.188, green: 0.82, blue: 0.345, alpha: 1)), ("red", NSColor(srgbRed: 1, green: 0.271, blue: 0.227, alpha: 1)), ("grey", NSColor(srgbRed: 0.557, green: 0.557, blue: 0.576, alpha: 1))]
    : [("green", NSColor(srgbRed: 0.204, green: 0.78, blue: 0.349, alpha: 1)), ("red", NSColor(srgbRed: 1, green: 0.231, blue: 0.188, alpha: 1)), ("grey", NSColor(srgbRed: 0.557, green: 0.557, blue: 0.576, alpha: 1))]

func rep(_ w: Int, _ h: Int) -> NSBitmapImageRep {
    NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: w, pixelsHigh: h, bitsPerSample: 8, samplesPerPixel: 4,
        hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
}
func draw(into r: NSBitmapImageRep, scale: CGFloat, _ body: () -> Void) {
    NSGraphicsContext.saveGraphicsState()
    let ctx = NSGraphicsContext(bitmapImageRep: r)!
    NSGraphicsContext.current = ctx
    ctx.cgContext.scaleBy(x: scale, y: scale)
    body()
    NSGraphicsContext.restoreGraphicsState()
}
/// A template image tinted with `color`, drawn in rect (points), rendered at the context's scale.
func drawTinted(_ img: NSImage, _ rect: NSRect, _ color: NSColor, scale: CGFloat) {
    let px = rep(Int(rect.width * scale), Int(rect.height * scale))
    draw(into: px, scale: scale) {
        img.draw(in: NSRect(origin: .zero, size: rect.size))
        color.set()
        NSRect(origin: .zero, size: rect.size).fill(using: .sourceIn)
    }
    px.draw(in: rect, from: .zero, operation: .sourceOver, fraction: 1, respectFlipped: false, hints: nil)
}
func symbol(_ name: String) -> NSImage {
    NSImage(systemSymbolName: name, accessibilityDescription: nil)!.withSymbolConfiguration(.init(pointSize: 14, weight: .regular))!
}

/// One menu bar strip (points): [our icon] [wifi] [battery] [clock]
func strip(scale: CGFloat, dotColor: NSColor) -> NSBitmapImageRep {
    let W: CGFloat = 250, H: CGFloat = 24
    let r = rep(Int(W * scale), Int(H * scale))
    draw(into: r, scale: scale) {
        bar.setFill(); NSRect(x: 0, y: 0, width: W, height: H).fill()
        let iconRect = NSRect(x: 12, y: 3, width: 18, height: 18)
        drawTinted(glyph, iconRect, fg, scale: scale)
        drawTinted(dot, iconRect, dotColor, scale: scale)
        var x: CGFloat = 46
        for name in ["wifi", "battery.75percent", "magnifyingglass"] {
            let s = symbol(name)
            drawTinted(s, NSRect(x: x, y: (H - s.size.height) / 2, width: s.size.width, height: s.size.height), fg, scale: scale)
            x += s.size.width + 16
        }
        ("Thu 19 Nov  20:15" as NSString).draw(at: NSPoint(x: x, y: 4), withAttributes: [.foregroundColor: fg, .font: NSFont.systemFont(ofSize: 13)])
    }
    return r
}
func icon(scale: CGFloat, dotColor: NSColor) -> NSBitmapImageRep {
    let r = rep(Int(24 * scale), Int(24 * scale))
    draw(into: r, scale: scale) {
        bar.setFill(); NSRect(x: 0, y: 0, width: 24, height: 24).fill()
        let rect = NSRect(x: 3, y: 3, width: 18, height: 18)
        drawTinted(glyph, rect, fg, scale: scale)
        drawTinted(dot, rect, dotColor, scale: scale)
    }
    return r
}

let SW = 1100, SH = 620
let sheet = rep(SW, SH)
draw(into: sheet, scale: 1) {
    (isDark ? NSColor(white: 0.08, alpha: 1) : NSColor(white: 0.99, alpha: 1)).setFill()
    NSRect(x: 0, y: 0, width: SW, height: SH).fill()
    let label = isDark ? NSColor(white: 0.75, alpha: 1) : NSColor(white: 0.35, alpha: 1)
    func put(_ r: NSBitmapImageRep, _ x: Int, _ yTop: Int, zoom: Int = 1) {
        let w = r.pixelsWide * zoom, h = r.pixelsHigh * zoom
        r.draw(in: NSRect(x: x, y: SH - yTop - h, width: w, height: h), from: .zero, operation: .copy, fraction: 1,
            respectFlipped: false, hints: [.interpolation: NSImageInterpolation.none.rawValue])
    }
    func text(_ s: String, _ x: Int, _ yTop: Int) {
        (s as NSString).draw(at: NSPoint(x: x, y: SH - yTop - 14), withAttributes: [.foregroundColor: label, .font: NSFont.systemFont(ofSize: 11)])
    }
    text("\(isDark ? "Dark" : "Light") menu bar, actual pixels: 1x (left) and 2x (right)", 20, 14)
    for (i, (name, c)) in dots.enumerated() {
        let y = 40 + i * 60
        text(name, 20, y + 8)
        put(strip(scale: 1, dotColor: c), 70, y)
        put(strip(scale: 2, dotColor: c), 340, y - 12)
    }
    text("Icon alone, nearest-neighbour zoom: 1x pixels at 8x, 2x pixels at 4x", 20, 230)
    for (i, (_, c)) in dots.enumerated() {
        put(icon(scale: 1, dotColor: c), 20 + i * 360, 255, zoom: 8)
        put(icon(scale: 2, dotColor: c), 20 + i * 360 + 180 - 4, 255, zoom: 4)
    }
}
try! sheet.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: a[3]))
