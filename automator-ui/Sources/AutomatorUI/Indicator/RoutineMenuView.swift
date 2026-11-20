import AppKit

/// A routine in the Routines submenu: its health dot and name, and its
/// summary in the core's words under it, at the menu's fixed width (both
/// truncate). It highlights like a menu item; a click does `onClick`.
@MainActor
final class RoutineMenuView: NSView {
    private static let inset: CGFloat = 14
    private static let dotSize: CGFloat = 8
    private static let height: CGFloat = 38

    private let name = NSTextField(labelWithString: "")
    private let summary = NSTextField(labelWithString: "")
    private let dot = NSView()
    private let onClick: () -> Void

    /// health: the core's verdict (ok, error, none).
    init(name: String, summary: String?, health: String?, onClick: @escaping () -> Void) {
        self.onClick = onClick
        super.init(frame: NSRect(x: 0, y: 0, width: MenuTextView.menuWidth, height: Self.height))
        autoresizingMask = [.width]
        let textX = Self.inset + Self.dotSize + 8
        let width = MenuTextView.menuWidth - textX - Self.inset

        dot.wantsLayer = true
        dot.layer?.cornerRadius = Self.dotSize / 2
        dot.layer?.backgroundColor =
            switch health {
            case "ok": NSColor.systemGreen.cgColor
            case "error": NSColor.systemRed.cgColor
            default: NSColor.systemGray.cgColor
            }
        dot.frame = NSRect(
            x: Self.inset, y: (Self.height - Self.dotSize) / 2, width: Self.dotSize, height: Self.dotSize)
        addSubview(dot)

        for (label, text, font) in [
            (self.name, name, NSFont.menuFont(ofSize: 0)),
            (self.summary, summary ?? "", NSFont.menuFont(ofSize: NSFont.smallSystemFontSize)),
        ] {
            label.stringValue = text
            label.font = font
            label.lineBreakMode = .byTruncatingTail
            label.maximumNumberOfLines = 1
            addSubview(label)
        }
        self.name.frame = NSRect(x: textX, y: 19, width: width, height: 17)
        self.summary.frame = NSRect(x: textX, y: 4, width: width, height: 15)
        toolTip = [name, summary].compactMap { $0 }.joined(separator: ": ")
        setAccessibilityLabel(toolTip)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("not used") }

    private var highlighted: Bool { enclosingMenuItem?.isHighlighted == true }

    override func viewWillDraw() {
        super.viewWillDraw()
        name.textColor = highlighted ? .selectedMenuItemTextColor : .labelColor
        summary.textColor = highlighted ? .selectedMenuItemTextColor : .secondaryLabelColor
    }

    override func draw(_ dirtyRect: NSRect) {
        guard highlighted else { return }
        NSColor.selectedContentBackgroundColor.setFill()
        NSBezierPath(roundedRect: bounds.insetBy(dx: 5, dy: 1), xRadius: 4, yRadius: 4).fill()
    }

    override func mouseUp(with event: NSEvent) {
        enclosingMenuItem?.menu?.cancelTracking()
        onClick()
    }
}
