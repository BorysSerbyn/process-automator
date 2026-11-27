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
    /// The installs whose template changed in this version, to override or
    /// not: opened when the app starts after an update changed some, over
    /// Updates (Back goes to what's new).
    case templateUpdates
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
        case .updates, .templateUpdates: .updates
        }
    }

    /// Whether it's a section's own screen.
    public var isRoot: Bool { home.root == self }

    /// Its name: the section's, or the routine's, hook's or watcher's.
    public var title: String {
        switch self {
        case .routine(let name), .hook(let name), .watcher(let name): name
        case .templateUpdates: "Template Updates"
        default: home.title
        }
    }
}

/// A place the window has been: a section, and what was opened in it on
/// top of its own screen (`path`, the last shown).
public struct Place: Equatable, Sendable {
    public var section: SidebarSection
    public var path: [Destination]

    public init(section: SidebarSection, path: [Destination] = []) {
        self.section = section
        self.path = path
    }

    /// What it shows.
    public var shown: Destination { path.last ?? section.root }

    /// It, with a destination gone and what was opened from it.
    func without(_ destination: Destination) -> Place {
        guard let i = path.firstIndex(of: destination) else { return self }
        return Place(section: section, path: Array(path[..<i]))
    }
}

/// Where the window is, and where it's been: a history of places, as a
/// browser's. Each move (the sidebar, a link, the menu) adds one, dropping
/// those gone back from; Back and Forward move through it.
public struct NavigationState: Equatable, Sendable {
    /// The places, oldest first, and the one it's at.
    public private(set) var history: [Place]
    public private(set) var at: Int

    /// How many places it keeps: the oldest go first.
    public static let historyLimit = 50

    public init(section: SidebarSection = .routines, path: [Destination] = []) {
        history = [Place(section: section, path: path)]
        at = 0
    }

    /// A window opened on a destination (from the menu, a notification,
    /// opening the app): it, and before it its section's own screen.
    public init(showing destination: Destination) {
        self.init(section: destination.home)
        show(destination)
    }

    public var place: Place { history[at] }
    public var section: SidebarSection { place.section }
    public var path: [Destination] { place.path }

    /// What it shows.
    public var shown: Destination { place.shown }

    /// Everything open: the section's screen and its path.
    public var open: [Destination] { [section.root] + path }

    public var canGoBack: Bool { at > 0 }
    public var canGoForward: Bool { at < history.count - 1 }

    /// The name of what Back goes to (nil: nothing to go back to).
    public var backTitle: String? { canGoBack ? history[at - 1].shown.title : nil }

    /// The name of what Forward goes to (nil: nothing to go forward to).
    public var forwardTitle: String? { canGoForward ? history[at + 1].shown.title : nil }

    /// Opened from outside the window (the menu, a notification, opening
    /// the app): its section, with only it opened there; Back goes to the
    /// section's own screen, then to where the window was.
    public mutating func show(_ destination: Destination) {
        let place = Place(section: destination.home, path: destination.isRoot ? [] : [destination])
        guard place != self.place else { return }
        if !destination.isRoot { go(to: Place(section: destination.home)) }
        go(to: place)
    }

    /// Opened from inside the window (Setup's routine to fix, a hook's
    /// watcher): on top of what shows, staying in the section. A section's
    /// own screen switches to it; one already open goes back to it.
    public mutating func push(_ destination: Destination) {
        if destination.isRoot {
            select(destination.home)
        } else if let i = path.firstIndex(of: destination) {
            go(to: Place(section: section, path: Array(path[...i])))
        } else {
            go(to: Place(section: section, path: path + [destination]))
        }
    }

    /// A section chosen in the sidebar: its own screen.
    public mutating func select(_ section: SidebarSection) {
        go(to: Place(section: section))
    }

    public mutating func back() {
        if canGoBack { at -= 1 }
    }

    public mutating func forward() {
        if canGoForward { at += 1 }
    }

    /// It's gone (deleted): it and what was opened from it close, wherever
    /// they are in the history; places left the same as the one before
    /// them are one.
    public mutating func removed(_ destination: Destination) {
        var kept: [Place] = []
        var keptAt = 0
        for (i, place) in history.enumerated() {
            let place = place.without(destination)
            if kept.last != place { kept.append(place) }
            if i == at { keptAt = kept.count - 1 }
        }
        history = kept
        at = keptAt
    }

    /// Moves to a place: after the one it's at, dropping those gone back
    /// from (nothing, if it's already there).
    private mutating func go(to place: Place) {
        guard place != self.place else { return }
        history = Array(history[...at].suffix(Self.historyLimit - 1)) + [place]
        at = history.count - 1
    }
}
