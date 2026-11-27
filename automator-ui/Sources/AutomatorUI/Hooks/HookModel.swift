import AutomatorUICore
import Combine
import SwiftUI

typealias HookInfo = Components.Schemas.HookInfo
typealias HookDetail = Components.Schemas.HookDetail
typealias WatcherInfo = Components.Schemas.WatcherInfo
typealias WatcherDetail = Components.Schemas.WatcherDetail
typealias WatcherReview = Components.Schemas.WatcherTrustOutput
typealias FireOutput = Components.Schemas.RunOutput

/// One hook as its window shows it: `hooks show`, its entry in run status,
/// its settings, runs, memories (an LLM hook's) and trust review, all as the
/// core gives them. It's a routine's window without a schedule or Run Now:
/// it fires with a sample event instead. Every change goes through the CLI,
/// then what changed is re-read.
@MainActor
final class HookModel: ObservableObject, RunHistoryModel, MemoriesModel {
    enum Tab: String, CaseIterable, Identifiable {
        case overview = "Overview"
        case runs = "Runs"
        case memories = "Memories"
        case settings = "Settings"
        var id: String { rawValue }
    }

    let name: String
    let cli: CLI
    @Published var tab: Tab = .overview
    @Published var detail: HookDetail?
    @Published var loadError: String?
    @Published var status: RunStatus?
    @Published var settings: [SettingState] = []
    @Published var errors: [String: String] = [:]
    @Published var runs: [RunSummary]?
    @Published var runsError: String?
    @Published var runEntries: [String: RunEntry] = [:]
    @Published var runErrors: [String: String] = [:]
    @Published var openRuns: Set<String> = []
    /// A run to bring into view in the Runs tab (from a notification), until it is.
    @Published var shownRun: String?
    @Published var memories: [Memory]?
    @Published var memoryError: String?
    @Published var trustReview: TrustReview?
    @Published var trustError: String?
    @Published var accessOpen = false
    /// Turning it on: its one review and confirm.
    let turnOn: TurnOn
    /// Grant… for the permissions it needs that aren't allowed.
    let granter: PermissionGranter
    /// Export Logs… and Report a Bug….
    let bugReport: BugReport
    /// Its detail is read again now and then while a permission it needs
    /// isn't allowed, so it turns allowed by itself.
    private var watchingPermissions = false
    /// The sign-in waiting for me in the browser: its name.
    @Published var signingIn: String?
    @Published var signInError: String?
    @Published var actionError: String?
    /// The control waiting for its command, or for its state to show.
    let pending = PendingAction()
    private var pendingChanges: AnyCancellable?
    var working: Bool { pending.busy }
    /// The sample event being written, for Fire.
    @Published var sampleEvent = ""
    /// A fire waiting for its run (a dry run, or for real).
    @Published var firing: Bool?
    /// How the last fire went: the run's result, or why it failed.
    @Published var fireResult: FireOutput?
    @Published var fireError: String?
    /// Called once the hook is deleted (its window closes).
    var onDeleted: () -> Void = {}
    /// Opened to turn it on (from the menu, a reason, the list's Turn On…):
    /// the review shows once it's read, if it needs turning on.
    var reviewWhenLoaded = false
    private var openedLatestRun = false

    init(name: String, cli: CLI, status: RunStatus?) {
        self.name = name
        self.cli = cli
        self.status = status
        turnOn = TurnOn(name: name, kind: .hook, cli: cli)
        granter = PermissionGranter(cli: cli, app: AppInfo.name)
        bugReport = BugReport(name: name, kind: .hook, cli: cli)
        pendingChanges = pending.objectWillChange.sink { [weak self] in self?.objectWillChange.send() }
        turnOn.turnedOn = { [weak self] in self?.load() }
        turnOn.granter.openURL = { NSWorkspace.shared.open($0) }
        granter.openURL = { NSWorkspace.shared.open($0) }
        bugReport.reveal = { NSWorkspace.shared.activateFileViewerSelecting([$0]) }
        granter.asked = { [weak self] in self?.loadDetail() }
    }

    /// Its entry in run status (health, summary, last run, problem).
    var entry: RoutineStatus? { status?.hook(name) }
    var running: Bool { [.running, .queued].contains(entry?.lastRun?.outcome) }
    var isProgram: Bool { detail?.isProgram ?? false }
    /// The tabs it has: a program hook has no memories.
    var tabs: [Tab] { Tab.allCases.filter { $0 != .memories || !isProgram } }

    func load() {
        loadDetail()
        loadSettings()
        loadRuns()
    }

    /// A new run status: when this hook's last run changed, its runs and
    /// detail are re-read.
    func update(_ status: RunStatus?) {
        defer { pending.settle() }
        let before = entry
        self.status = status
        if entry?.lastRun != before?.lastRun || entry?.problem != before?.problem {
            loadDetail()
            loadRuns()
            Task { await turnOn.statusChanged() }
        }
    }

    /// Reads its detail again every few seconds while a permission it needs
    /// isn't allowed (the scheduler records it within a pass).
    private func watchPermissions() {
        guard !watchingPermissions else { return }
        watchingPermissions = true
        Task { [weak self] in
            while true {
                try? await Task.sleep(for: .seconds(3))
                // A window closed meanwhile is let go: it's held only weakly.
                guard let self else { return }
                guard !(self.detail?.permissions ?? []).blocking.isEmpty else {
                    self.watchingPermissions = false
                    return
                }
                self.loadDetail()
            }
        }
    }

    private func loadDetail() {
        Task {
            switch await cli.background({ [name] in $0.hook(name) }) {
            case .success(let detail):
                defer { pending.settle() }
                granter.saw(detail.permissions ?? [])
                if !(detail.permissions ?? []).blocking.isEmpty { watchPermissions() }
                if self.detail == nil, sampleEvent.isEmpty { sampleEvent = HookWords.sampleEvent(detail.on) }
                self.detail = detail
                loadError = nil
                if !detail.isProgram, memories == nil, memoryError == nil { loadMemories() }
                if reviewWhenLoaded {
                    reviewWhenLoaded = false
                    if detail.turnOn == true { turnOn.start() }
                }
            case .failure(let failure):
                loadError = failure.message
            }
        }
    }

    private func loadSettings() {
        Task {
            switch await cli.background({ [name] in $0.routineSettings(name) }) {
            case .success(let list): settings = list
            case .failure(let failure): loadError = failure.message
            }
        }
    }

    func loadRuns() {
        Task {
            switch await cli.background({ [name] in $0.runs(name, limit: 50) }) {
            case .success(let list):
                runs = list
                runsError = nil
                if !openedLatestRun, let latest = list.first {
                    openedLatestRun = true
                    openRun(latest.id)
                }
            case .failure(let failure):
                runsError = failure.message
            }
        }
    }

    func toggleRun(_ id: String) {
        withAnimation(.easeInOut(duration: 0.18)) {
            if openRuns.remove(id) == nil { openRun(id) }
        }
    }

    /// Shows one run: the Runs tab, with that run open and in view.
    func showRun(_ id: String) {
        tab = .runs
        openRun(id)
        shownRun = id
    }

    private func openRun(_ id: String) {
        openRuns.insert(id)
        guard runEntries[id] == nil else { return }
        Task {
            switch await cli.background({ $0.run(id) }) {
            case .success(let entry):
                runEntries[id] = entry
                runErrors[id] = nil
            case .failure(let failure):
                runErrors[id] = failure.message
            }
        }
    }

    func openReport(_ id: String) {
        Task {
            if let failure = await cli.background({ $0.openRun(id) }) { actionError = failure.message }
        }
    }

    // MARK: Controls

    /// Stop: "Stopping…" until it no longer runs.
    func stop() {
        let stopped: @MainActor () -> Bool = { [weak self] in !(self?.running ?? false) }
        control("stop", "Stopping…", until: stopped) { [name] in $0.stop(name).map { _ in () } }
    }

    func setPaused(_ paused: Bool) {
        control(
            paused ? "pause" : "resume", paused ? "Pausing…" : "Resuming…",
            until: { [weak self] in self?.detail?.enabled != paused },
            { [name] in $0.setHookPaused(name, paused).map { _ in () } }, then: load)
    }

    func delete() {
        control("delete", "Deleting…", { [name] in $0.deleteHook(name).map { _ in () } }, then: { self.onDeleted() })
    }

    /// Runs a control's command (`action`, its button saying `label`
    /// meanwhile), then `then`; it stays pending until `until` shows in its
    /// state (nil: until the command returns), at most PendingAction's
    /// timeout. Its failure shows as `actionError`.
    private func control(
        _ action: String, _ label: String, until: (@MainActor () -> Bool)? = nil,
        _ work: @escaping @Sendable (CLI) -> Result<Void, CLI.Failure>, then: @escaping () -> Void = {}
    ) {
        let cli = cli
        Task {
            let failure = await pending.run(action, label: label, until: until) {
                await cli.background(work).failureMessage
            }
            actionError = failure
            if failure == nil { then() }
        }
    }

    /// Fires it with the sample event: the core runs it as its watcher's
    /// event would, and says how it went. A real one is in its runs after.
    func fire(dryRun: Bool) {
        firing = dryRun
        fireResult = nil
        fireError = nil
        let event = sampleEvent
        Task {
            let result = await cli.background { [name] in $0.fireHook(name, event: event, dryRun: dryRun) }
            firing = nil
            switch result {
            case .success(let output): fireResult = output
            case .failure(let failure): fireError = failure.message
            }
            if !dryRun { loadRuns() }
        }
    }

    // MARK: Settings

    func set(_ key: String, _ text: String) {
        apply(key) { $0.setSetting(key, text) }
    }

    func reset(_ key: String) {
        apply(key) { $0.resetSetting(key) }
    }

    private func apply(_ key: String, _ change: @escaping @Sendable (CLI) -> Result<SettingState, CLI.Failure>) {
        Task {
            if case .failure(let failure) = await cli.background(change) {
                errors[key] = failure.message
            } else {
                errors[key] = nil
            }
            loadSettings()
            loadDetail()
        }
    }

    // MARK: Memories

    func loadMemories() {
        Task {
            switch await cli.background({ [name] in $0.memories(name) }) {
            case .success(let list): memories = list
            case .failure(let failure): memoryError = failure.message
            }
        }
    }

    func addMemory(_ text: String) async -> Bool {
        switch await cli.background({ [name] in $0.addMemory(text, routine: name) }) {
        case .success:
            memoryError = nil
            loadMemories()
            return true
        case .failure(let failure):
            memoryError = failure.message
            return false
        }
    }

    func removeMemory(_ id: String) {
        Task {
            memoryError = await cli.background({ $0.removeMemory(id) })?.message
            loadMemories()
        }
    }

    // MARK: Trust

    func toggleAccess() {
        withAnimation(.easeInOut(duration: 0.18)) { accessOpen.toggle() }
        if accessOpen, trustReview == nil { loadTrust() }
    }

    func loadTrust() {
        Task {
            switch await cli.background({ [name] in $0.hookTrustCheck(name) }) {
            case .success(let review):
                trustReview = review
                trustError = nil
            case .failure(let failure):
                trustError = failure.message
            }
        }
    }

    /// Signs in where the review (or a program hook's secrets) says, in
    /// the browser, then reads what's stored, and the review, again.
    func signIn(_ signInName: String) {
        signingIn = signInName
        Task {
            let failure = await cli.background { $0.signIn(signInName) }
            signingIn = nil
            signInError = failure?.message
            loadDetail()
            if trustReview != nil { loadTrust() }
        }
    }

    /// Turn On…: its review, in a sheet, read afresh.
    func startTurnOn() { turnOn.start() }
}
