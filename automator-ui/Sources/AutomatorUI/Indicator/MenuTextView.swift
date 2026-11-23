import AppKit

/// A line of text in the menu at the menu's fixed width: it wraps to at most
/// `lines` lines, then ends in an ellipsis, so no text ever widens the menu.
/// The full text (and detail) shows on hover; one with `onClick` is a
/// normal menu item: it highlights under the pointer and a click (or Return)
/// does it, the first time. Its NSMenuItem needs a target and action, or the
/// menu treats it as disabled: neither highlighting it nor handing it the
/// first click (`menuItem`).
@MainActor
final class MenuTextView: NSView {
    /// The menu's width: every text line is laid out to it, and the menu
    /// keeps it as its minimum, so it neither grows nor shrinks with its text.
    static let menuWidth: CGFloat = 240
    private static let inset = NSSize(width: 14, height: 3)

    private let onClick: (() -> Void)?
    private let label: NSTextField
    private let dimmed: Bool

    init(text: String, detail: String? = nil, lines: Int, dimmed: Bool = false, onClick: (() -> Void)? = nil) {
        self.onClick = onClick
        let font = NSFont.menuFont(ofSize: 0)
        let width = Self.menuWidth - 2 * Self.inset.width
        self.dimmed = dimmed
        let label = NSTextField(wrappingLabelWithString: text)
        label.font = font
        label.textColor = dimmed ? .secondaryLabelColor : .labelColor
        label.maximumNumberOfLines = lines
        label.lineBreakMode = .byTruncatingTail
        label.cell?.truncatesLastVisibleLine = true
        label.preferredMaxLayoutWidth = width
        let lineHeight = NSLayoutManager().defaultLineHeight(for: font)
        let height = min(ceil(label.intrinsicContentSize.height), ceil(lineHeight * CGFloat(lines)))
        label.frame = NSRect(x: Self.inset.width, y: Self.inset.height, width: width, height: height)
        self.label = label
        super.init(frame: NSRect(x: 0, y: 0, width: Self.menuWidth, height: height + 2 * Self.inset.height))
        autoresizingMask = [.width]
        addSubview(label)
        toolTip = [text, detail].compactMap { $0 }.joined(separator: "\n\n")
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("not used") }

    /// Its menu item: enabled, with an action that does `onClick` (chosen
    /// from the keyboard, too), when it has one.
    func menuItem() -> NSMenuItem {
        let item = NSMenuItem()
        item.view = self
        if let onClick {
            let action = MenuAction(onClick)
            item.target = action
            item.action = #selector(MenuAction.invoke)
            item.representedObject = action  // the target is weak: held here
        }
        return item
    }

    private var highlighted: Bool { onClick != nil && enclosingMenuItem?.isHighlighted == true }

    // The label is only drawing: every click is the row's, so none lands on
    // a text field that ignores it.
    override func hitTest(_ point: NSPoint) -> NSView? {
        onClick != nil && frame.contains(point) ? self : super.hitTest(point)
    }

    override func viewWillDraw() {
        super.viewWillDraw()
        label.textColor =
            highlighted ? .selectedMenuItemTextColor : (dimmed ? .secondaryLabelColor : .labelColor)
    }

    override func draw(_ dirtyRect: NSRect) {
        guard highlighted else { return }
        NSColor.selectedContentBackgroundColor.setFill()
        NSBezierPath(roundedRect: bounds.insetBy(dx: 5, dy: 1), xRadius: 4, yRadius: 4).fill()
    }

    override func mouseUp(with event: NSEvent) {
        guard let onClick else { return }
        enclosingMenuItem?.menu?.cancelTracking()
        onClick()
    }
}

/// A menu item's action that runs a closure.
@MainActor
final class MenuAction: NSObject {
    private let run: () -> Void

    init(_ run: @escaping () -> Void) { self.run = run }

    @objc func invoke() { run() }
}
