import AutomatorUICore
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
    @Published var signingIn = false
    @Published var signInError: String?
    @Published var actionError: String?
    @Published var working = false
    /// The sample event being written, for Fire.
    @Published var sampleEvent = ""
    /// A fire waiting for its run (a dry run, or for real).
    @Published var firing: Bool?
    /// How the last fire went: the run's result, or why it failed.
    @Published var fireResult: FireOutput?
    @Published var fireError: String?
    /// Called once the hook is deleted (its window closes).
    var onDeleted: () -> Void = {}
    private var openedLatestRun = false

    init(name: String, cli: CLI, status: RunStatus?) {
        self.name = name
        self.cli = cli
        self.status = status
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
        let before = entry
        self.status = status
        if entry?.lastRun != before?.lastRun || entry?.problem != before?.problem {
            loadDetail()
            loadRuns()
        }
    }

    private func loadDetail() {
        Task {
            switch await cli.background({ [name] in $0.hook(name) }) {
            case .success(let detail):
                if self.detail == nil, sampleEvent.isEmpty { sampleEvent = HookWords.sampleEvent(detail.on) }
                self.detail = detail
                loadError = nil
                if !detail.isProgram, memories == nil, memoryError == nil { loadMemories() }
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

    func stop() {
        control { [name] in $0.stop(name).map { _ in () } }
    }

    func setPaused(_ paused: Bool) {
        control({ [name] in $0.setHookPaused(name, paused).map { _ in () } }, then: load)
    }

    func delete() {
        control({ [name] in $0.deleteHook(name).map { _ in () } }, then: { self.onDeleted() })
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

    /// Signs in to a program hook's secrets in the browser, then reads
    /// what's stored again.
    func signIn() {
        signingIn = true
        Task {
            let failure = await cli.background { [name] in $0.signIn(name) }
            signingIn = false
            signInError = failure?.message
            loadDetail()
            if trustReview != nil { loadTrust() }
        }
    }

    /// Trusts it with what it acts on and can reach; `turnOn` also turns it
    /// on (an off hook).
    func trust(turnOn: Bool) {
        working = true
        Task {
            let result = await cli.background { [name] in $0.trustHook(name, turnOn: turnOn) }
            working = false
            switch result {
            case .success(let review):
                trustReview = review
                trustError = nil
                loadDetail()
                loadSettings()
            case .failure(let failure):
                trustError = failure.message
            }
        }
    }
}
