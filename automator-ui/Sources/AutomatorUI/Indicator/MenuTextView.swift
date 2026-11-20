import AppKit

/// A line of text in the menu at the menu's fixed width: it wraps to at most
/// `lines` lines, then ends in an ellipsis, so no text ever widens the menu.
/// The full text (and detail) shows on hover; a click does `onClick`, when
/// there is one.
@MainActor
final class MenuTextView: NSView {
    /// The menu's width: every text line is laid out to it, and the menu
    /// keeps it as its minimum, so it neither grows nor shrinks with its text.
    static let menuWidth: CGFloat = 240
    private static let inset = NSSize(width: 14, height: 3)

    private let onClick: (() -> Void)?

    init(text: String, detail: String? = nil, lines: Int, dimmed: Bool = false, onClick: (() -> Void)? = nil) {
        self.onClick = onClick
        let font = NSFont.menuFont(ofSize: 0)
        let width = Self.menuWidth - 2 * Self.inset.width
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
        super.init(frame: NSRect(x: 0, y: 0, width: Self.menuWidth, height: height + 2 * Self.inset.height))
        autoresizingMask = [.width]
        addSubview(label)
        toolTip = [text, detail].compactMap { $0 }.joined(separator: "\n\n")
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("not used") }

    override func mouseUp(with event: NSEvent) {
        guard let onClick else { return }
        enclosingMenuItem?.menu?.cancelTracking()
        onClick()
    }
}
