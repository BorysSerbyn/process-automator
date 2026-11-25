import Foundation

/// A run's tool-call record as the run's page shows it.
extension Components.Schemas.Call {
    /// What it returned, or its error: what the record kept.
    public var output: String? {
        if let error, !error.isEmpty { return error }
        if let result, !result.isEmpty { return result }
        return nil
    }

    /// Whether it failed.
    public var failed: Bool { error?.isEmpty == false }

    /// What was clipped, when the record didn't keep all of what it returned.
    public var truncatedText: String? {
        guard truncated == true else { return nil }
        let kept = (output ?? "").utf8.count
        guard let size = resultSize, size > 0 else { return "Clipped: the run's record keeps only so much." }
        return "Clipped: \(Self.bytes(kept)) of \(Self.bytes(size)) kept."
    }

    private static func bytes(_ n: Int) -> String {
        n < 1024 ? "\(n) bytes" : "\((n + 512) / 1024) KiB"
    }
}
