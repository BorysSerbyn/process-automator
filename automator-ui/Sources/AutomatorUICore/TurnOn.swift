import Combine
import Foundation

/// Turning something on (an LLM routine, a program, a hook): one review,
/// read from `trust --check`, and one confirm, `trust --yes --resume` (a
/// hook's also trusts its watcher). Sign-ins the review says are still
/// needed are done from it. Every failure is kept where it happened, for
/// the review to show: nothing fails silently.
@MainActor
public final class TurnOn: ObservableObject {
    public enum Kind: Sendable { case routine, hook }

    public let name: String
    public let kind: Kind
    let cli: CLI
    /// Whether the review shows (a sheet, or setup's step).
    @Published public var showing = false
    @Published public private(set) var review: Components.Schemas.TrustOutput?
    /// Why the review couldn't be read.
    @Published public private(set) var loadError: String?
    /// The confirm is waiting for the core, and why it failed.
    @Published public private(set) var working = false
    @Published public private(set) var error: String?
    /// The sign-in waiting for me in the browser (its name), and why the
    /// last one failed.
    @Published public private(set) var signingIn: String?
    @Published public private(set) var signInError: String?
    /// Called once it's on (the window reads everything again).
    public var turnedOn: () -> Void = {}
    /// Grant… for the permissions the review says are missing.
    public let granter: PermissionGranter
    /// How often the review is read again while it shows a permission
    /// that isn't allowed yet, so the row turns allowed by itself (nil:
    /// never; then only a status change reads it again).
    public var pollEvery: Duration? = .seconds(2)

    public init(name: String, kind: Kind, cli: CLI, app: String = AppInfo.name) {
        self.name = name
        self.kind = kind
        self.cli = cli
        granter = PermissionGranter(cli: cli, app: app)
        granter.asked = { [weak self] in Task { await self?.load() } }
    }

    /// Turn On…: the review shows, read afresh, and read again while it
    /// waits for a permission.
    public func start() {
        error = nil
        showing = true
        Task {
            await load()
            await poll()
        }
    }

    /// Run status changed (the scheduler recorded a permission, say): an
    /// open review is read again.
    public func statusChanged() async {
        if showing { await load() }
    }

    /// Whether the review shows a permission that isn't allowed yet.
    public var waitingForPermission: Bool { !(review?.permissions ?? []).blocking.isEmpty }

    private func poll() async {
        guard let every = pollEvery else { return }
        while showing, waitingForPermission {
            try? await Task.sleep(for: every)
            guard showing else { return }
            await load()
        }
    }

    /// Reads the review (`routines trust --check`, `hooks trust --check`).
    public func load() async {
        loadError = nil
        let result = await cli.background { [name, kind] cli in
            kind == .hook ? cli.hookTrustCheck(name) : cli.trustCheck(name)
        }
        switch result {
        case .success(let review): self.review = review
        case .failure(let failure): loadError = failure.message
        }
    }

    /// The one confirm: trusts what the review showed and turns it on. The
    /// review closes once it's on; why it couldn't stays in it.
    public func confirm() async {
        working = true
        error = nil
        let result = await cli.background { [name, kind] cli in
            kind == .hook ? cli.trustHook(name, turnOn: true) : cli.turnOn(name)
        }
        working = false
        switch result {
        case .success(let review):
            self.review = review
            showing = false
            turnedOn()
        case .failure(let failure):
            error = failure.message
        }
    }

    /// Signs in where the review says one is still needed (`auth login
    /// <name> --browser`, waiting while I do), then reads the review again.
    public func signIn(_ signInName: String) async {
        signingIn = signInName
        let failure = await cli.background { $0.signIn(signInName) }
        signingIn = nil
        signInError = failure?.message
        await load()
    }
}
