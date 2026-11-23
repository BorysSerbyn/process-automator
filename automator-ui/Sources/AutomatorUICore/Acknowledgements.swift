import Foundation

/// Notifications a routine or hook asked me to acknowledge (notify_me with
/// acknowledge): the core keeps them waiting, and health red, until I do.
/// Its reason ("(2) Acknowledgements pending", `acknowledge`) is the menu's
/// status line, opening a window that lists them; the notification itself
/// has an Acknowledge button.
extension CoreNotification {
    /// Whether it waits for me to acknowledge it: it's posted with an
    /// Acknowledge button.
    public var needsAcknowledging: Bool { acknowledge == true && acknowledgedAt == nil }
}

extension CLI {
    /// The notifications waiting for me to acknowledge them, oldest first
    /// (`notifications list --waiting`).
    public func waitingNotifications() -> Result<[CoreNotification], Failure> {
        call([CoreNotification].self, ["notifications", "list", "--waiting"])
    }

    /// Acknowledges notifications (`notifications ack <id>...`).
    public func acknowledge(_ ids: [String]) -> Result<Components.Schemas.AckOutput, Failure> {
        call(Components.Schemas.AckOutput.self, ["notifications", "ack"] + ids)
    }

    /// Acknowledges every notification waiting (`notifications ack --all`).
    public func acknowledgeAll() -> Result<Components.Schemas.AckOutput, Failure> {
        call(Components.Schemas.AckOutput.self, ["notifications", "ack", "--all"])
    }
}

/// The Acknowledgements window's list: the notifications waiting, read
/// again when run status says what waits changed; Acknowledge on each and
/// Acknowledge All, each failure shown where its button is.
@MainActor
public final class Acknowledgements: ObservableObject {
    let cli: CLI
    @Published public private(set) var waiting: [CoreNotification] = []
    @Published public private(set) var loaded = false
    @Published public private(set) var loadError: String?
    /// The notifications being acknowledged ("*": all of them), and why
    /// the last one failed.
    @Published public private(set) var acknowledging: Set<String> = []
    @Published public private(set) var error: String?
    private var shownIDs: [String]?

    public init(cli: CLI) { self.cli = cli }

    public func load() async {
        let result = await cli.background { $0.waitingNotifications() }
        switch result {
        case .success(let list):
            waiting = list
            loadError = nil
        case .failure(let failure):
            loadError = failure.message
        }
        loaded = true
    }

    /// A new run status: the list is read again when what waits changed.
    public func update(_ status: RunStatus?) async {
        let ids = status?.waiting?.map(\.id) ?? []
        guard ids != shownIDs else { return }
        shownIDs = ids
        await load()
    }

    public func acknowledge(_ id: String) async {
        await ack(id) { $0.acknowledge([id]).map { _ in () } }
    }

    public func acknowledgeAll() async {
        await ack("*") { $0.acknowledgeAll().map { _ in () } }
    }

    private func ack(_ key: String, _ work: @escaping @Sendable (CLI) -> Result<Void, CLI.Failure>) async {
        acknowledging.insert(key)
        error = nil
        let result = await cli.background(work)
        acknowledging.remove(key)
        if case .failure(let failure) = result { error = failure.message }
        await load()
    }
}
