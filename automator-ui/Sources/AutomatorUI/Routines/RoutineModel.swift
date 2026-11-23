import AutomatorUICore
import SwiftUI

/// One routine as its window shows it: `routines show`, its entry in run
/// status, its settings, schedule, runs, memories and trust review, all as the
/// core gives them. Every change goes through the CLI, then what changed is
/// re-read.
@MainActor
final class RoutineModel: ObservableObject, RunHistoryModel, MemoriesModel {
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
    @Published var detail: RoutineDetail?
    @Published var loadError: String?
    @Published var status: RunStatus?
    @Published var settings: [SettingState] = []
    /// The core's message for each setting it refused a value for.
    @Published var errors: [String: String] = [:]
    /// The current schedule described (`schedule describe`).
    @Published var schedule: ScheduleInfo?
    @Published var runs: [RunSummary]?
    @Published var runsError: String?
    /// Runs in full, read when first opened.
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
    /// Its detail is read again now and then while a permission it needs
    /// isn't allowed, so it turns allowed by itself.
    private var watchingPermissions = false
    /// The sign-in waiting for me in the browser (`auth login <name>
    /// --browser`): its name.
    @Published var signingIn: String?
    @Published var signInError: String?
    /// Why the last control (run, stop, pause, delete) failed.
    @Published var actionError: String?
    /// A control is waiting for the core.
    @Published var working = false
    /// Called once the routine is deleted (its window closes).
    var onDeleted: () -> Void = {}
    /// Opened to turn it on (from the menu, a reason, a list's Turn On…):
    /// the review shows once it's read, if it needs turning on.
    var reviewWhenLoaded = false
    private var openedLatestRun = false

    init(name: String, cli: CLI, status: RunStatus?) {
        self.name = name
        self.cli = cli
        self.status = status
        turnOn = TurnOn(name: name, kind: .routine, cli: cli)
        granter = PermissionGranter(cli: cli, app: AppInfo.name)
        turnOn.turnedOn = { [weak self] in self?.load() }
        turnOn.granter.openURL = { NSWorkspace.shared.open($0) }
        granter.openURL = { NSWorkspace.shared.open($0) }
        granter.asked = { [weak self] in self?.loadDetail() }
    }

    /// Its entry in run status (health, summary, last and next run, problem).
    var entry: RoutineStatus? { status?.routine(name) }
    /// The schedule setting (type cron), shown with the picker, not as a row.
    var scheduleSetting: SettingState? { settings.first { $0._type == .cron } }
    var otherSettings: [SettingState] { settings.filter { $0._type != .cron } }
    /// A run in progress or waiting for its turn: Stop replaces Run Now.
    var running: Bool { [.running, .queued].contains(entry?.lastRun?.outcome) }
    /// A program: only LLM routines have memories.
    var isProgram: Bool { detail?.isProgram ?? false }
    /// The tabs it has.
    var tabs: [Tab] { Tab.allCases.filter { $0 != .memories || !isProgram } }

    func load() {
        loadDetail()
        loadSettings()
        loadRuns()
    }

    /// A new run status: when this routine's last run changed, its runs and
    /// detail are re-read.
    func update(_ status: RunStatus?) {
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
            switch await cli.background({ [name] in $0.routine(name) }) {
            case .success(let detail):
                granter.saw(detail.permissions ?? [])
                if !(detail.permissions ?? []).blocking.isEmpty { watchPermissions() }
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
            case .success(let list):
                settings = list
                describeSchedule()
            case .failure(let failure):
                loadError = failure.message
            }
        }
    }

    private func describeSchedule() {
        guard let cron = scheduleSetting?.valueText, !cron.isEmpty else {
            schedule = nil
            return
        }
        Task {
            schedule = try? await cli.background({ $0.describeSchedule(cron) }).get()
        }
    }

    func loadRuns() {
        Task {
            switch await cli.background({ [name] in $0.runs(name, limit: 50) }) {
            case .success(let list):
                runs = list
                runsError = nil
                // The latest run starts open: its output is what's usually wanted.
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

    func runNow(dryRun: Bool) {
        control { [name] in $0.start(name, dryRun: dryRun).map { _ in () } }
    }

    func stop() {
        control { [name] in $0.stop(name).map { _ in () } }
    }

    func setPaused(_ paused: Bool) {
        control({ [name] in $0.setRoutinePaused(name, paused).map { _ in () } }, then: load)
    }

    func delete() {
        control({ [name] in $0.deleteRoutine(name).map { _ in () } }, then: { self.onDeleted() })
    }

    private func control(
        _ work: @escaping @Sendable (CLI) -> Result<Void, CLI.Failure>, then: @escaping () -> Void = {}
    ) {
        working = true
        Task {
            let result = await cli.background(work)
            working = false
            switch result {
            case .success:
                actionError = nil
                then()
            case .failure(let failure):
                actionError = failure.message
            }
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
            let result = await cli.background(change)
            if case .failure(let failure) = result {
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

    /// Adds a memory; true when the core took it (the field then clears).
    func addMemory(_ text: String) async -> Bool {
        let result = await cli.background { [name] in $0.addMemory(text, routine: name) }
        switch result {
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
            if let failure = await cli.background({ $0.removeMemory(id) }) {
                memoryError = failure.message
            } else {
                memoryError = nil
            }
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
            switch await cli.background({ [name] in $0.trustCheck(name) }) {
            case .success(let review):
                trustReview = review
                trustError = nil
            case .failure(let failure):
                trustError = failure.message
            }
        }
    }

    /// Signs in where the review (or a program's secrets) says: the core
    /// opens a page in the browser to enter them, as setup's sign-in step
    /// does. Then what's stored, and the review, are read again.
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
