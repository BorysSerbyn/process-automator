import Foundation
import Testing

@testable import AutomatorUICore

@Suite struct ReleaseNotesTests {
    @Test func blocks() {
        let notes = """
            ## What's new
            - **Faster** starts
              - nested
            * another

            A paragraph
            on two lines.
            ---
            **Full Changelog**: https://github.com/o/r/compare/v1.0.0...v1.1.0
            """
        #expect(
            ReleaseNotes.blocks(notes) == [
                .heading("What's new"),
                .bullet("**Faster** starts", depth: 0),
                .bullet("nested", depth: 1),
                .bullet("another", depth: 0),
                .paragraph("A paragraph on two lines."),
                .paragraph("**Full Changelog**: https://github.com/o/r/compare/v1.0.0...v1.1.0"),
            ])
        #expect(ReleaseNotes.blocks("").isEmpty)
    }

    @Test func inlineLinks() {
        let text = ReleaseNotes.inline(
            "**Full Changelog**: https://github.com/o/r/compare/v1...v2 and [docs](https://example.com)")
        #expect(String(text.characters) == "Full Changelog: https://github.com/o/r/compare/v1...v2 and docs")
        let links = text.runs.compactMap(\.link).map(\.absoluteString)
        #expect(links == ["https://github.com/o/r/compare/v1...v2", "https://example.com"])
    }
}
