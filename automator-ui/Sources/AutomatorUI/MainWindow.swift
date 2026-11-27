import AppKit
import AutomatorUICore
import SwiftUI

/// The app's window: a sidebar of sections and what the section shows, with
/// Back and Forward through where it's been (the bar's buttons, ⌘[ and ⌘],
/// the mouse's back and forward buttons, a swipe). Every screen is a
/// destination in it (NavigationState says which shows); the menu, a
/// notification and opening the app open theirs with `show`, the screens'
/// own links with `push`. A destination's data is read when it's shown and
/// freed when it's left; the window and all of it are freed when it closes
/// (SwiftUI holds a lot). New run status reaches whatever is open.
@MainActor
final class MainWindow: NSObject, NSWindowDelegate, ObservableObject {
    private let cli: CLI
    let updates: Updates
    private let changed: () -> Void
    private var window: NSWindow?
    /// Hears the mouse's back and forward buttons and swipes in the window
    /// while it's open.
    private var mouseMonitor: Any?

    @Published private(set) var nav = NavigationState()
    @Published private(set) var status: RunStatus?

    private var routinesModel: RoutinesModel?
    private var hooksModel: HooksModel?
    private var acknowledgementsModel: Acknowledgements?
    private var setupModel: SetupViewModel?
    private var settingsModel: SettingsModel?
    private var templateUpdatesModel: TemplateUpdates?
    /// The settings groups open when Settings was last left: its model is
    /// freed then, this outlives it.
    private var settingsOpenGroups: Set<String>?
    private var routineModels: [String: RoutineModel] = [:]
    private var hookModels: [String: HookModel] = [:]
    private var watcherModels: [String: WatcherModel] = [:]

    /// changed: a setup action succeeded (run status is up to date: show it).
    init(cli: CLI, updates: Updates, changed: @escaping () -> Void) {
        self.cli = cli
        self.updates = updates
        self.changed = changed
    }

    // MARK: Opening

    /// Opens a destination from outside the window (the menu, a
    /// notification, opening the app): in its section, the window in front.
    /// `run` shows one of a routine's or hook's runs; `review` its Turn On
    /// review once it's read, if it needs turning on.
    func show(_ destination: Destination, run: String? = nil, review: Bool = false) {
        let opening = window == nil
        makeWindow()
        go(to: destination, run: run, review: review) {
            if opening { $0 = NavigationState(showing: destination) } else { $0.show(destination) }
        }
        bringToFront(window)
    }

    /// Opens Template Updates when templates something was installed from
    /// changed (the app starts after every update): asked in the background.
    func offerTemplateUpdates() {
        Task {
            guard case .success(let updates) = await cli.background({ $0.outdatedTemplates() }), !updates.isEmpty
            else { return }
            show(.templateUpdates)
        }
    }

    /// Closes Template Updates, once chosen: back to Updates.
    private func closeTemplateUpdates() {
        var next = nav
        next.removed(.templateUpdates)
        apply(next)
    }

    /// Opens a destination from inside the window: on top of what shows.
    func push(_ destination: Destination, run: String? = nil, review: Bool = false) {
        go(to: destination, run: run, review: review) { $0.push(destination) }
    }

    /// A section chosen in the sidebar.
    func select(_ section: SidebarSection) {
        guard section != nav.section || !nav.path.isEmpty else { return }
        go(to: section.root) { $0.select(section) }
    }

    func back() {
        var next = nav
        next.back()
        apply(next)
    }

    func forward() {
        var next = nav
        next.forward()
        apply(next)
    }

    /// Where the window goes: the destination it opens is read (again, if
    /// it was open), with its run or review.
    private func go(
        to destination: Destination, run: String? = nil, review: Bool = false,
        _ change: (inout NavigationState) -> Void
    ) {
        var next = nav
        change(&next)
        apply(next, except: destination)
        load(destination, run: run, review: review)
    }

    /// Moves to next: the models of what's open there made and read (but
    /// except's: the caller reads it), the others let go.
    private func apply(_ next: NavigationState, except: Destination? = nil) {
        for destination in next.open where makeModel(destination) && destination != except {
            load(destination, run: nil, review: false)
        }
        let open = Set(next.open)
        if !open.contains(.routines) { routinesModel = nil }
        if !open.contains(.hooks) { hooksModel = nil }
        if !open.contains(.acknowledgements) { acknowledgementsModel = nil }
        if !open.contains(.setup) { setupModel = nil }
        // Another screen shown over it skips nothing: they're offered at the
        // next launch (only closing the window, or Not now, is choosing).
        if !open.contains(.templateUpdates) { templateUpdatesModel = nil }
        if !open.contains(.settings), let model = settingsModel {
            settingsOpenGroups = model.expanded
            settingsModel = nil
        }
        routineModels = routineModels.filter { open.contains(.routine($0.key)) }
        hookModels = hookModels.filter { open.contains(.hook($0.key)) }
        watcherModels = watcherModels.filter { open.contains(.watcher($0.key)) }
        nav = next
        window?.subtitle = nav.shown.isRoot ? "" : nav.shown.title
    }

    /// Makes the model a destination shows, if it isn't made: whether it made it.
    private func makeModel(_ destination: Destination) -> Bool {
        switch destination {
        case .routines:
            guard routinesModel == nil else { return false }
            routinesModel = RoutinesModel(cli: cli, status: status)
        case .hooks:
            guard hooksModel == nil else { return false }
            hooksModel = HooksModel(cli: cli, status: status)
        case .acknowledgements:
            guard acknowledgementsModel == nil else { return false }
            acknowledgementsModel = Acknowledgements(cli: cli)
        case .setup:
            guard setupModel == nil else { return false }
            let model = SetupViewModel(
                cli: cli, openRoutine: { [weak self] in self?.push(.routine($0)) }, changed: changed)
            frontOnGrant(model.granter)
            setupModel = model
        case .settings:
            guard settingsModel == nil else { return false }
            settingsModel = SettingsModel(cli: cli, openGroups: settingsOpenGroups)
        case .updates:
            return false  // the Updates model lives as long as the app: the menu shows it
        case .templateUpdates:
            guard templateUpdatesModel == nil else { return false }
            templateUpdatesModel = TemplateUpdates(cli: cli)
        case .routine(let name):
            guard routineModels[name] == nil else { return false }
            let model = RoutineModel(name: name, cli: cli, status: status)
            model.onDeleted = { [weak self] in self?.deleted(.routine(name)) }
            frontOnGrant(model.granter, model.turnOn.granter)
            routineModels[name] = model
        case .hook(let name):
            guard hookModels[name] == nil else { return false }
            let model = HookModel(name: name, cli: cli, status: status)
            model.onDeleted = { [weak self] in self?.deleted(.hook(name)) }
            frontOnGrant(model.granter, model.turnOn.granter)
            hookModels[name] = model
        case .watcher(let name):
            guard watcherModels[name] == nil else { return false }
            let model = WatcherModel(name: name, cli: cli, status: status)
            model.onDeleted = { [weak self] in self?.deleted(.watcher(name)) }
            frontOnGrant(model.granter)
            watcherModels[name] = model
        }
        return true
    }

    /// Reads what a destination shows (again), as opening its window did.
    private func load(_ destination: Destination, run: String?, review: Bool) {
        switch destination {
        case .routines: routinesModel?.load()
        case .hooks: hooksModel?.load()
        case .acknowledgements:
            if let model = acknowledgementsModel { Task { await model.load() } }
        case .setup: setupModel?.load()
        case .settings: settingsModel?.load()  // what changed elsewhere since it was last shown
        case .updates: updates.opened()
        case .templateUpdates:
            if let model = templateUpdatesModel { Task { await model.load() } }
        case .routine(let name):
            guard let model = routineModels[name] else { return }
            if review { model.reviewWhenLoaded = true }
            model.load()
            if let run { model.showRun(run) }
        case .hook(let name):
            guard let model = hookModels[name] else { return }
            if review { model.reviewWhenLoaded = true }
            model.load()
            if let run { model.showRun(run) }
        case .watcher(let name): watcherModels[name]?.load()
        }
    }

    /// Deleted: it closes (Back to what it was opened from), and the lists
    /// are read again.
    private func deleted(_ destination: Destination) {
        var next = nav
        next.removed(destination)
        apply(next)
        routinesModel?.load()
        hooksModel?.load()
    }

    /// Brings the window back to the front once a permission its Grant…
    /// asked for is allowed (bringToFront says why).
    private func frontOnGrant(_ granters: PermissionGranter...) {
        for granter in granters {
            granter.granted = { [weak self] _ in bringToFront(self?.window) }
        }
    }

    // MARK: Run status

    func update(_ status: RunStatus?) {
        self.status = status
        routinesModel?.update(status)
        hooksModel?.update(status)
        for model in routineModels.values { model.update(status) }
        for model in hookModels.values { model.update(status) }
        for model in watcherModels.values { model.update(status) }
        if let model = acknowledgementsModel { Task { await model.update(status) } }
    }

    // MARK: The window

    private func makeWindow() {
        guard window == nil else { return }
        let controller = NSHostingController(rootView: MainView(main: self, updates: updates.model))
        // The screens' minimum sizes hold the window's, but their ideal
        // sizes don't move it: it stays the size I give it.
        controller.sizingOptions = [.minSize]
        let window = NSWindow(contentViewController: controller)
        window.title = AppInfo.name
        window.isReleasedWhenClosed = false
        window.setContentSize(NSSize(width: 960, height: 760))
        window.center()
        window.delegate = self
        self.window = window
        watchMouse()
    }

    /// The mouse's back and forward buttons (3 and 4, counting from 0) and
    /// a swipe left or right (as Safari's, when swiping between pages is
    /// set to three fingers) go back and forward in the window.
    private func watchMouse() {
        mouseMonitor = NSEvent.addLocalMonitorForEvents(matching: [.otherMouseDown, .swipe]) { [weak self] event in
            guard let self, let window = self.window, event.window === window else { return event }
            let back: Bool
            switch event.type {
            case .otherMouseDown where event.buttonNumber == 3: back = true
            case .otherMouseDown where event.buttonNumber == 4: back = false
            case .swipe where event.deltaX != 0: back = event.deltaX > 0
            default: return event
            }
            if back { self.back() } else { self.forward() }
            return nil
        }
    }

    func windowWillClose(_ notification: Notification) {
        // Let go once the close is done, unless it was reopened meanwhile:
        // it opens fresh next time.
        DispatchQueue.main.async {
            guard self.window?.isVisible == false else { return }
            self.window = nil
            if let monitor = self.mouseMonitor { NSEvent.removeMonitor(monitor) }
            self.mouseMonitor = nil
            self.settingsOpenGroups = self.settingsModel?.expanded ?? self.settingsOpenGroups
            self.routinesModel = nil
            self.hooksModel = nil
            self.acknowledgementsModel = nil
            self.setupModel = nil
            self.settingsModel = nil
            if let model = self.templateUpdatesModel { self.leftUndecided(model) }
            self.templateUpdatesModel = nil
            self.routineModels = [:]
            self.hookModels = [:]
            self.watcherModels = [:]
            self.nav = NavigationState()
        }
    }

    /// Template Updates closed with the window without choosing: the same as
    /// Not now, so they aren't offered again. One overriding finishes on its
    /// own.
    private func leftUndecided(_ model: TemplateUpdates) {
        guard model.undecided else { return }
        Task {
            if !(await model.notNow()) { NSLog("Couldn't skip the template updates: %@", model.error ?? "") }
        }
    }

    // MARK: What each destination shows

    /// The screen a destination shows: the view its window had, its links
    /// opening their destinations here.
    @ViewBuilder func content(for destination: Destination) -> some View {
        switch destination {
        case .routines:
            if let model = routinesModel {
                RoutinesListView(
                    model: model,
                    openRoutine: { [weak self] in self?.push(.routine($0)) },
                    turnOn: { [weak self] in self?.push(.routine($0), review: true) })
            }
        case .hooks:
            if let model = hooksModel {
                HooksListView(
                    model: model, openHook: { [weak self] in self?.push(.hook($0)) },
                    turnOn: { [weak self] in self?.push(.hook($0), review: true) },
                    openWatcher: { [weak self] in self?.push(.watcher($0)) })
            }
        case .acknowledgements:
            if let model = acknowledgementsModel {
                AcknowledgementsView(model: model) { [weak self] name, run, hook in
                    self?.push(hook ? .hook(name) : .routine(name), run: run)
                }
            }
        case .setup:
            if let model = setupModel { SetupView(model: model) }
        case .settings:
            if let model = settingsModel { SettingsView(model: model) }
        case .updates:
            ManageUpdatesView(model: updates.model)
        case .templateUpdates:
            if let model = templateUpdatesModel {
                TemplateUpdatesView(model: model, close: { [weak self] in self?.closeTemplateUpdates() })
            }
        case .routine(let name):
            if let model = routineModels[name] { RoutineDetailView(model: model) }
        case .hook(let name):
            if let model = hookModels[name] {
                HookDetailView(model: model, openWatcher: { [weak self] in self?.push(.watcher($0)) })
            }
        case .watcher(let name):
            if let model = watcherModels[name] {
                WatcherView(model: model, openHook: { [weak self] in self?.push(.hook($0)) })
            }
        }
    }
}

/// The window's content: the sidebar, always shown, a bar with Back and
/// Forward, and the destination itself.
struct MainView: View {
    @ObservedObject var main: MainWindow
    @ObservedObject var updates: ManageUpdatesModel

    var body: some View {
        HStack(spacing: 0) {
            sidebar.frame(width: 200)
            Divider()
            VStack(spacing: 0) {
                bar
                Divider()
                main.content(for: main.nav.shown)
                    .id(main.nav.shown)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .background(Color(nsColor: .windowBackgroundColor))
    }

    private var sidebar: some View {
        List(selection: Binding(get: { main.nav.section }, set: { if let section = $0 { main.select(section) } })) {
            ForEach(Array(SidebarSection.groups.enumerated()), id: \.offset) { _, group in
                Section {
                    ForEach(group, id: \.self) { section in
                        Label(section.title, systemImage: section.systemImage)
                            .badge(badge(section).map { Text($0) })
                            .tag(section)
                    }
                }
            }
        }
        .listStyle(.sidebar)
    }

    /// Beside a section: what waits in it (Updates': a newer release, or one installing).
    private func badge(_ section: SidebarSection) -> String? {
        guard section == .updates else { return section.badge(for: main.status) }
        if updates.installing { return "Updating" }
        return updates.status?.updateAvailable == true ? "New" : nil
    }

    /// Back and Forward, always there in the same place (dimmed when
    /// there's nowhere to go), each saying where it goes.
    private var bar: some View {
        HStack(spacing: 4) {
            arrow("chevron.left", to: main.nav.backTitle, verb: "Back", key: "[", action: main.back)
            arrow("chevron.right", to: main.nav.forwardTitle, verb: "Forward", key: "]", action: main.forward)
            Spacer()
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
    }

    private func arrow(
        _ symbol: String, to title: String?, verb: String, key: KeyEquivalent, action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 14, weight: .semibold))
                .frame(width: 28, height: 24)
                .contentShape(Rectangle())
        }
        .buttonStyle(.borderless)
        .disabled(title == nil)
        .keyboardShortcut(key, modifiers: .command)
        .help(title.map { "\(verb) to \($0) (⌘\(key.character))" } ?? verb)
        .accessibilityLabel(verb)
    }
}

extension SidebarSection {
    var systemImage: String {
        switch self {
        case .routines: "clock"
        case .hooks: "bolt"
        case .acknowledgements: "checkmark.bubble"
        case .setup: "checklist"
        case .settings: "gearshape"
        case .updates: "arrow.down.circle"
        }
    }
}

/// Shows a window in front, from a menu choice. It waits for the menu to
/// finish closing, and orders the window front even if macOS doesn't
/// activate the app (a menu bar app's activation is only a request), so the
/// first click never leaves the window behind the frontmost app. It's also
/// how the window comes back once a permission its Grant… asked for is
/// allowed: macOS gives the focus back to the app that was in front before
/// its prompt, and an app with no Dock icon doesn't get it back by itself.
@MainActor
func bringToFront(_ window: NSWindow?) {
    DispatchQueue.main.async {
        NSApp.activate()
        window?.makeKeyAndOrderFront(nil)
        window?.orderFrontRegardless()
    }
}
