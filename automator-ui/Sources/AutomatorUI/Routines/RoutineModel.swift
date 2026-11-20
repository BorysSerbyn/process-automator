import AutomatorUICore
import SwiftUI

/// One routine as its window shows it: `routines show`, its entry in run
/// status, its settings, schedule, runs, memories and trust review, all as the
/// core gives them. Every change goes through the CLI, then what changed is
/// re-read.
@MainActor
final class RoutineModel: ObservableObject {
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
    @Published var memories: [Memory]?
    @Published var memoryError: String?
    @Published var trustReview: TrustReview?
    @Published var trustError: String?
    @Published var accessOpen = false
    /// Why the last control (run, stop, pause, delete) failed.
    @Published var actionError: String?
    /// A control is waiting for the core.
    @Published var working = false
    /// Called once the routine is deleted (its window closes).
    var onDeleted: () -> Void = {}
    private var openedLatestRun = false

    init(name: String, cli: CLI, status: RunStatus?) {
        self.name = name
        self.cli = cli
        self.status = status
    }

    /// Its entry in run status (health, summary, last and next run, problem).
    var entry: RoutineStatus? { status?.routine(name) }
    /// The schedule setting (type cron), shown with the picker, not as a row.
    var scheduleSetting: SettingState? { settings.first { $0._type == .cron } }
    var otherSettings: [SettingState] { settings.filter { $0._type != .cron } }
    /// A run in progress or waiting for its turn: Stop replaces Run Now.
    var running: Bool { [.running, .queued].contains(entry?.lastRun?.outcome) }

    func load() {
        loadDetail()
        loadSettings()
        loadRuns()
        loadMemories()
    }

    /// A new run status: when this routine's last run changed, its runs and
    /// detail are re-read.
    func update(_ status: RunStatus?) {
        let before = entry?.lastRun
        self.status = status
        if entry?.lastRun != before {
            loadDetail()
            loadRuns()
        }
    }

    private func loadDetail() {
        Task {
            switch await cli.background({ [name] in $0.routine(name) }) {
            case .success(let detail):
                self.detail = detail
                loadError = nil
                accessOpen = accessOpen || !detail.trusted
                if !detail.trusted, trustReview == nil { loadTrust() }
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

    func trust() {
        working = true
        Task {
            let result = await cli.background { [name] in $0.trust(name) }
            working = false
            switch result {
            case .success(let review):
                trustReview = review
                trustError = nil
                loadDetail()
            case .failure(let failure):
                trustError = failure.message
            }
        }
    }
}
