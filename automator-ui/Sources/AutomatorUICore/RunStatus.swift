import Foundation

/// The run-status file (contracts/run-status.schema.json), generated from the Go core's types.
public typealias RunStatus = Components.Schemas.RunStatus

extension RunStatus {
    public static let supportedSchemaVersion = 1

    public static func decode(_ data: Data) throws -> RunStatus {
        try CoreJSON.decoder().decode(RunStatus.self, from: data)
    }

    /// Loads the file. Missing, unreadable or unsupported → nil.
    public static func load(from url: URL) -> RunStatus? {
        guard let data = try? Data(contentsOf: url),
            let status = try? decode(data),
            status.schemaVersion == supportedSchemaVersion
        else { return nil }
        return status
    }

    /// What the menu bar dot shows.
    public enum Light: Equatable, Sendable { case green, red, grey }

    /// Whether the dot pulses: the core says a routine is running, and its status
    /// is still valid (an expired status shows red, not activity).
    public static func pulsing(for status: RunStatus?, now: Date = Date()) -> Bool {
        guard let status, responding(status, now: now) else { return false }
        return status.busy
    }

    /// Whether the core is responding: its status is readable and not past
    /// `validUntil`. automator-ui's only judgement; everything else it shows
    /// is the core's (health, reasons).
    public static func responding(_ status: RunStatus?, now: Date = Date()) -> Bool {
        guard let status, let validUntil = status.validUntil else { return false }
        return now <= validUntil
    }

    /// The dot shows the core's verdict as is. The only thing automator-ui
    /// judges is whether the core is responding: no readable status, or one
    /// past its `validUntil`, is red.
    public static func light(for status: RunStatus?, now: Date = Date()) -> Light {
        guard let status, responding(status, now: now) else { return .red }
        switch status.health {
        case .ok: return .green
        case .error: return .red
        case .none: return .grey
        }
    }
}
