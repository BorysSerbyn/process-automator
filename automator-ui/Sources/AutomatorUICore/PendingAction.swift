import Combine
import Foundation

/// A button's action from the moment it's clicked until what it waits for
/// shows: Run Now until run status says the routine runs (or its run ended
/// already), Stop until it no longer runs, Pause until it's paused. While
/// one is pending, the button shows it ("Starting…") and every control it
/// guards is disabled, so it can't be clicked twice. It ends when its
/// condition holds (checked on every run status), when the command fails
/// (its error shows as before), or after `timeout`, showing things as they
/// are.
@MainActor
public final class PendingAction: ObservableObject {
    /// What's pending: the action's name ("run", "stop", …) and what the
    /// button says meanwhile.
    public struct Pending: Equatable, Sendable {
        public let action: String
        public let label: String
    }

    @Published public private(set) var pending: Pending?
    /// How long it waits for its state before giving up.
    public var timeout: Duration = .seconds(30)
    private var until: (@MainActor () -> Bool)?
    private var token = 0

    public init() {}

    /// Whether `action` is the one pending.
    public func isPending(_ action: String) -> Bool { pending?.action == action }

    /// Whether any action is pending: the controls it guards are disabled.
    public var busy: Bool { pending != nil }

    /// Starts an action: `work` runs the command; once it returns, the
    /// action stays pending until `until` holds (nil: it's done when the
    /// command returns). A failure ends it at once and is returned, for the
    /// caller to show as before.
    @discardableResult
    public func run(
        _ action: String, label: String, until: (@MainActor () -> Bool)? = nil,
        _ work: @escaping @Sendable () async -> String?
    ) async -> String? {
        token += 1
        let mine = token
        pending = Pending(action: action, label: label)
        self.until = nil
        let failure = await work()
        guard mine == token else { return failure }
        if failure != nil || until == nil {
            end()
            return failure
        }
        self.until = until
        settle()
        if pending != nil {
            let timeout = timeout
            Task { [weak self] in
                try? await Task.sleep(for: timeout)
                guard let self, self.token == mine else { return }
                self.end()
            }
        }
        return nil
    }

    /// Run status changed: a pending action whose state shows now ends.
    public func settle() {
        if let until, until() { end() }
    }

    private func end() {
        pending = nil
        until = nil
        token += 1
    }
}

extension Result where Failure == CLI.Failure {
    /// Why it failed, in the core's words (nil: it didn't).
    public var failureMessage: String? {
        if case .failure(let failure) = self { return failure.message }
        return nil
    }
}
