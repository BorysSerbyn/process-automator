import Foundation

/// What opening and quitting the app do, besides showing it: the scheduler
/// is started when the app opens and stopped when it quits (both start again
/// at the next login), through the CLI. The Linux tray does the same.
public enum Lifecycle {
    /// The window opening the app shows while it's already running.
    public enum Window: Equatable, Sendable { case setup, settings }

    /// Setup while the core's status lists setup steps still to do, else
    /// Settings. An unreadable status lists none.
    public static func reopenWindow(for status: RunStatus?) -> Window {
        status?.setupLeft?.isEmpty == false ? .setup : .settings
    }

    /// The app opened (launched, or opened again while running): the
    /// scheduler started if it isn't running. Why it couldn't, if it
    /// couldn't (run status then shows it isn't responding; doctor says why).
    public static func opened(_ cli: CLI) async -> CLI.Failure? {
        if case .failure(let failure) = await cli.background({ $0.startScheduler() }) { return failure }
        return nil
    }

    /// Quit: the scheduler stopped, then `terminate`; when stopping fails,
    /// `failed` is told why first, and it terminates anyway.
    @MainActor
    public static func quit(
        _ cli: CLI?, failed: @MainActor (CLI.Failure) -> Void, terminate: @MainActor () -> Void
    ) async {
        if let cli, case .failure(let failure) = await cli.background({ $0.stopApp() }) {
            failed(failure)
        }
        terminate()
    }
}
