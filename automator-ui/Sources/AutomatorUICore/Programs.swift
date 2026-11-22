import Foundation

/// A program's facts as its window puts them: words for what the core says,
/// nothing worked out.
extension Components.Schemas.ProgramInfo {
    /// What Dry Run does with it, from `writes`: a program that writes is told
    /// to change nothing; one that doesn't runs as it is.
    public var dryRunText: String {
        writes
            ? "Dry Run passes it --dry-run: it's told to change nothing."
            : "It doesn't write: Dry Run runs it as it is."
    }

    /// A dry run needs trust, as a real run does.
    public static let dryRunTrust = "Like a real run, a dry run needs it trusted."

    /// The start of its SHA-256, enough to tell copies apart (the window copies
    /// the whole of it); nil when the core gave none.
    public var shortChecksum: String? {
        guard let checksum, !checksum.isEmpty else { return nil }
        return checksum.count > 12 ? "\(checksum.prefix(12))…" : checksum
    }

    /// Whether a secret it declares isn't stored: it needs a sign-in
    /// (`auth login <program>`).
    public var needsSignIn: Bool { secrets.contains { !$0.stored } }
}

extension Components.Schemas.Execution {
    /// What the output shown is, when the core kept only part of it.
    public var truncatedText: String? {
        outputTruncated == true
            ? "It printed more than is kept: this is its start and its end, with what was left out marked between them."
            : nil
    }
}
