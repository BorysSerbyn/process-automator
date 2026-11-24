import Foundation

/// The app's window: a sidebar of sections, each with a stack of what was
/// opened from it (a routine from the Routines list, a watcher from a hook).
/// What it shows is a NavigationState; the Linux windows app follows the
/// same rules (automator-tray/windows/internal/nav).
public enum SidebarSection: String, CaseIterable, Hashable, Sendable {
    case routines, hooks, acknowledgements, setup, settings, updates

    /// Its name in the sidebar.
    public var title: String {
        switch self {
        case .routines: "Routines"
        case .hooks: "Hooks"
        case .acknowledgements: "Acknowledgements"
        case .setup: "Setup"
        case .settings: "Settings"
        case .updates: "Updates"
        }
    }

    /// What the section shows before anything is opened from it.
    public var root: Destination {
        switch self {
        case .routines: .routines
        case .hooks: .hooks
        case .acknowledgements: .acknowledgements
        case .setup: .setup
        case .settings: .settings
        case .updates: .updates
        }
    }

    /// The sidebar's groups: what runs, then the app's own.
    public static let groups: [[SidebarSection]] = [
        [.routines, .hooks, .acknowledgements], [.setup, .settings, .updates],
    ]

    /// What the sidebar shows beside it, from run status: how many
    /// notifications wait to be acknowledged, how many setup steps are left
    /// (nil: nothing). Updates' is the menu's note, which run status doesn't hold.
    public func badge(for status: RunStatus?) -> String? {
        switch self {
        case .acknowledgements:
            let n = status?.waiting?.count ?? 0
            return n > 0 ? "\(n)" : nil
        case .setup:
            let n = status?.setupLeft?.count ?? 0
            return n > 0 ? "\(n) to do" : nil
        default: return nil
        }
    }
}

/// Something the window shows: a section's own screen, or one routine's,
/// hook's or watcher's.
public enum Destination: Hashable, Sendable {
    case routines, hooks, acknowledgements, setup, settings, updates
    case routine(String)
    case hook(String)
    case watcher(String)

    /// The section it's shown in when opened from outside the window (the
    /// menu, a notification): a routine under Routines, a hook or watcher
    /// under Hooks.
    public var home: SidebarSection {
        switch self {
        case .routines, .routine: .routines
        case .hooks, .hook, .watcher: .hooks
        case .acknowledgements: .acknowledgements
        case .setup: .setup
        case .settings: .settings
        case .updates: .updates
        }
    }

    /// Whether it's a section's own screen.
    public var isRoot: Bool { home.root == self }

    /// Its name: the section's, or the routine's, hook's or watcher's.
    public var title: String {
        switch self {
        case .routine(let name), .hook(let name), .watcher(let name): name
        default: home.title
        }
    }
}

/// Where the window is: a section, and what was opened in it on top of its
/// own screen (`path`, the last shown; Back goes to the one before).
public struct NavigationState: Equatable, Sendable {
    public private(set) var section: SidebarSection
    public var path: [Destination]

    public init(section: SidebarSection = .routines, path: [Destination] = []) {
        self.section = section
        self.path = path
    }

    /// What it shows.
    public var shown: Destination { path.last ?? section.root }

    /// Everything open: the section's screen and its path.
    public var open: [Destination] { [section.root] + path }

    public var canGoBack: Bool { !path.isEmpty }

    /// Opened from outside the window (the menu, a notification, opening
    /// the app): its section, with only it opened there.
    public mutating func show(_ destination: Destination) {
        section = destination.home
        path = destination.isRoot ? [] : [destination]
    }

    /// Opened from inside the window (Setup's routine to fix, a hook's
    /// watcher): on top of what shows, staying in the section. A section's
    /// own screen switches to it; one already open goes back to it.
    public mutating func push(_ destination: Destination) {
        if destination.isRoot {
            select(destination.home)
        } else if let i = path.firstIndex(of: destination) {
            path.removeSubrange((i + 1)...)
        } else {
            path.append(destination)
        }
    }

    /// A section chosen in the sidebar: its own screen.
    public mutating func select(_ section: SidebarSection) {
        self.section = section
        path = []
    }

    public mutating func back() {
        if !path.isEmpty { path.removeLast() }
    }

    /// It's gone (deleted): it and what was opened from it close.
    public mutating func removed(_ destination: Destination) {
        if let i = path.firstIndex(of: destination) { path.removeSubrange(i...) }
    }
}
