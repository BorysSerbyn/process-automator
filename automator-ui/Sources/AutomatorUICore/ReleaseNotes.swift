import Foundation

/// A release's notes (Markdown, as its GitHub release has them), in blocks
/// a window lays out: headings, bullets and paragraphs, each line's inline
/// Markdown (bold, code, links) kept for the window to render.
public enum ReleaseNotes {
    public enum Block: Equatable, Sendable {
        case heading(String)
        case bullet(String, depth: Int)
        case paragraph(String)
    }

    /// The notes' blocks: a `#` line is a heading, a `-`, `*` or `+` line a
    /// bullet (indented two spaces a level), consecutive other lines one
    /// paragraph; blank lines and rules end a paragraph.
    public static func blocks(_ markdown: String) -> [Block] {
        var blocks: [Block] = []
        var paragraph: [String] = []
        func flush() {
            if !paragraph.isEmpty { blocks.append(.paragraph(paragraph.joined(separator: " "))) }
            paragraph = []
        }
        for raw in markdown.replacingOccurrences(of: "\r\n", with: "\n").split(
            separator: "\n", omittingEmptySubsequences: false)
        {
            let line = String(raw)
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.isEmpty || trimmed.allSatisfy({ $0 == "-" || $0 == "*" || $0 == "_" }) && trimmed.count >= 3 {
                flush()
            } else if trimmed.hasPrefix("#") {
                flush()
                blocks.append(.heading(trimmed.drop(while: { $0 == "#" }).trimmingCharacters(in: .whitespaces)))
            } else if let marker = trimmed.first, "-*+".contains(marker), trimmed.dropFirst().first == " " {
                flush()
                let indent = line.prefix(while: { $0 == " " }).count
                blocks.append(
                    .bullet(String(trimmed.dropFirst(2)).trimmingCharacters(in: .whitespaces), depth: indent / 2))
            } else {
                paragraph.append(trimmed)
            }
        }
        flush()
        return blocks
    }

    /// A line's inline Markdown (bold, italics, code, links), or the text as
    /// is when it doesn't parse; bare web addresses (GitHub's "Full
    /// Changelog: https://…") are links too.
    public static func inline(_ text: String) -> AttributedString {
        var out =
            (try? AttributedString(
                markdown: text, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)))
            ?? AttributedString(text)
        let plain = String(out.characters)
        guard let detector = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue) else {
            return out
        }
        for match in detector.matches(in: plain, range: NSRange(plain.startIndex..., in: plain)) {
            guard let url = match.url, let range = Range(match.range, in: plain),
                let lower = AttributedString.Index(range.lowerBound, within: out),
                let upper = AttributedString.Index(range.upperBound, within: out),
                out[lower..<upper].link == nil
            else { continue }
            out[lower..<upper].link = url
        }
        return out
    }
}
