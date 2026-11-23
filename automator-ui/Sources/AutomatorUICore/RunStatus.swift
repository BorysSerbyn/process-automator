import Foundation

/// The core's run status (`status --json`, contracts/run-status.schema.json),
/// generated from the Go core's types. automator-ui follows it through the
/// CLI (StatusStream), never the file.
public typealias RunStatus = Components.Schemas.RunStatus

extension RunStatus {
    public static let supportedSchemaVersion = 1

    public static func decode(_ data: Data) throws -> RunStatus {
        try CoreJSON.decoder().decode(RunStatus.self, from: data)
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

/// One reason the dot is red, in the core's words.
public typealias RunStatusReason = Components.Schemas.RunStatusReason

extension RunStatusReason {
    /// What choosing a reason opens.
    public enum Target: Equatable, Sendable {
        /// The Acknowledgements window: notifications waiting for me
        /// ("(2) Acknowledgements pending").
        case acknowledgements
        /// The Setup window: setup's steps still to do, among them turning
        /// on what isn't trusted, one after another.
        case setup
        /// A watcher's window: what's wrong is the watcher's, for every hook on it.
        case watcher(String)
        /// A hook's window, on its run if it names one.
        case hook(String, run: String?)
        /// The run's report.
        case run(String)
        /// A routine's window (nothing ran: it isn't trusted, say).
        case routine(String)
    }

    /// What choosing it opens, from what it's about (nil: nothing).
    public var target: Target? {
        if acknowledge == true { return .acknowledgements }
        if setup == true { return .setup }
        if let watcher, !watcher.isEmpty { return .watcher(watcher) }
        if hook == true, let routine, !routine.isEmpty { return .hook(routine, run: run) }
        if let run, !run.isEmpty { return .run(run) }
        if let routine, !routine.isEmpty { return .routine(routine) }
        return nil
    }
}
