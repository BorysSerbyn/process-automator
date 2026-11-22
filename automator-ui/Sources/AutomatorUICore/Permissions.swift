import Combine
import Foundation

/// The OS permissions something needs, as its review and its window put
/// them: words for what the core says (`permissions` in a review or a
/// `show`), and Grant…, which has the scheduler ask the OS (`permissions
/// request`) and, while it's still not allowed, opens the page in the OS's
/// settings where it's switched on.
public typealias PermissionNeed = Components.Schemas.PermissionNeed

extension PermissionNeed {
    /// Whether it keeps what needs it from running: the core's missing and
    /// not-checked. Unknown (can't be checked) and unsupported (not on this
    /// OS) never do, and aren't shown as needing anything.
    public var blocking: Bool { state == .missing || state == .not_hyphen_checked }

    /// Whether it's shown at all: granted and blocking ones are; unknown and
    /// unsupported aren't.
    public var shown: Bool { state != .unknown && state != .unsupported }

    /// Why it's needed, from what declares it: "for its watcher home-key",
    /// "for the notify plugin", "for its program".
    public var reason: String {
        let parts = by.map { source -> String in
            switch source.kind {
            case .watcher: "its watcher \(source.name)"
            case .plugin: "the \(source.name) plugin"
            case .program, .hook: "its program"
            }
        }
        return parts.isEmpty ? "" : "for " + parts.joined(separator: " and ")
    }

    /// Its state in words.
    public var stateText: String {
        switch state {
        case .granted: "Allowed"
        case .missing: "Not allowed"
        case .not_hyphen_checked: "Not checked yet"
        case .unknown: "Can't be checked"
        case .unsupported: "Not on this computer"
        }
    }
}

extension [PermissionNeed] {
    /// The ones keeping it from running.
    public var blocking: [PermissionNeed] { filter(\.blocking) }

    /// "It won't run until Input Monitoring and Notifications are allowed."
    /// (nil: none is missing).
    public var wontRunText: String? {
        let labels = blocking.map(\.label)
        guard let last = labels.last else { return nil }
        let names = labels.count == 1 ? last : labels.dropLast().joined(separator: ", ") + " and " + last
        return "It won't run until \(names) \(labels.count == 1 ? "is" : "are") allowed."
    }
}

extension Components.Schemas.PermissionRequest {
    /// Whether it's allowed now.
    public var granted: Bool { state == .granted }

    /// What to do next while it isn't allowed: "Switch on Acme in System
    /// Settings > Privacy & Security > Input Monitoring." (nil once it is).
    public func instructions(app: String) -> String? {
        guard !granted else { return nil }
        let place = _where.map { " \($0)" } ?? ""
        let asked = asked ? "Answer the prompt, or switch" : "Switch"
        return "\(asked) on \(app)\(place)."
    }
}

extension CLI {
    /// Has the scheduler ask the OS for a permission, as the app (`permissions
    /// request`): the first time, macOS shows its prompt naming the app.
    public func requestPermission(_ permission: String) -> Result<Components.Schemas.PermissionRequest, Failure> {
        call(Components.Schemas.PermissionRequest.self, ["permissions", "request", permission])
    }
}

/// Grant… for a permission, wherever it shows (a review, a window): asks
/// through the scheduler, then, while it's still not allowed, opens the
/// OS's page for it and says what to switch on. Why it failed shows where
/// the button is.
@MainActor
public final class PermissionGranter: ObservableObject {
    let cli: CLI
    /// The app's name, in what to switch on.
    let app: String
    /// The permission being asked for.
    @Published public private(set) var granting: String?
    /// What to do next, and why asking failed, by permission.
    @Published public private(set) var notes: [String: String] = [:]
    @Published public private(set) var errors: [String: String] = [:]
    /// Opens an address (the OS's settings page); the app's is NSWorkspace's.
    public var openURL: (URL) -> Void = { _ in }
    /// Called after asking (what needs it reads its permissions again).
    public var asked: () -> Void = {}

    public init(cli: CLI, app: String) {
        self.cli = cli
        self.app = app
    }

    public func grant(_ permission: String) async {
        granting = permission
        errors[permission] = nil
        let result = await cli.background { $0.requestPermission(permission) }
        granting = nil
        switch result {
        case .success(let request):
            notes[permission] = request.instructions(app: app)
            if !request.granted, let address = request.settingsUrl, let url = URL(string: address) {
                openURL(url)
            }
        case .failure(let failure):
            errors[permission] = failure.message
        }
        asked()
    }
}
