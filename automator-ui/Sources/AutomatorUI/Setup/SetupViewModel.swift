import AutomatorUICore
import SwiftUI

typealias SetupStep = Components.Schemas.SetupStep
typealias TemplateInfo = Components.Schemas.TemplateInfo

/// First-run setup as the core sees it (`setup --json`: every step, what's
/// done, what to ask), and doing each step through the CLI. After each, the
/// steps are read again: the core decides what's done.
@MainActor
final class SetupViewModel: ObservableObject {
    let cli: CLI
    @Published var steps: [SetupStep] = []
    @Published var done = false
    /// How many required steps aren't done, as the core counts them.
    @Published var remaining = 0
    @Published var loaded = false
    @Published var loadError: String?
    /// The core's message when a step's action failed, by step id.
    @Published var errors: [String: String] = [:]
    /// Steps waiting for the core (a sign-in waits for the browser).
    @Published var working: Set<String> = []
    /// Steps opened or closed by hand; the others are open while not done.
    @Published var toggled: [String: Bool] = [:]

    @Published var templates: [TemplateInfo]?
    @Published var chosen: Set<String> = []
    /// The projects picked (none at first): only their templates, and
    /// those for any project, are offered.
    @Published var projects: Set<String> = []
    /// Sign-ins whose preparation the user said is done (by step id).
    @Published var prepared: Set<String> = []
    /// Parameter values typed for each chosen template.
    @Published var values: [String: [String: String]] = [:]
    /// The model backend setting (its choices and words come from the core).
    @Published var backend: SettingState?
    /// The settings the chosen backend needs set (the step's `settings`, e.g.
    /// the local backend's server and model), in the core's order.
    @Published var backendSettings: [SettingState] = []
    @Published var reviews: [String: TrustReview] = [:]
    /// Why a routine's review couldn't be read, by routine.
    @Published var reviewErrors: [String: String] = [:]
    /// A sign-in a review asked for, waiting for me in the browser (its
    /// name), and why the last one failed.
    @Published var signingIn: String?
    @Published var signInError: String?
    /// Steps the core does without asking (`auto`) that were started once
    /// already: a failed one waits for its button.
    private var autoStarted: Set<String> = []

    /// Opens a routine's window (a routine to fix).
    let openRoutine: (String) -> Void
    /// Called after an action succeeds: the core has updated run status, and
    /// the menu should show it now.
    let changed: () -> Void
    private var generation = 0

    init(cli: CLI, openRoutine: @escaping (String) -> Void = { _ in }, changed: @escaping () -> Void = {}) {
        self.cli = cli
        self.openRoutine = openRoutine
        self.changed = changed
    }

    static func id(_ step: SetupStep) -> String { step.step.rawValue + ":" + (step.subject ?? "") }

    /// Steps start open while they're to do; optional ones start closed.
    func isOpen(_ step: SetupStep) -> Bool { toggled[Self.id(step)] ?? (!step.done && step.optional != true) }

    /// Groups start open while any of their steps isn't done.
    func isOpen(_ group: SetupGroup) -> Bool { toggled[group.id] ?? !group.allDone }

    func toggle(_ group: SetupGroup) {
        let open = !isOpen(group)
        withAnimation(.easeInOut(duration: 0.18)) { toggled[group.id] = open }
    }

    func toggle(_ step: SetupStep) {
        let open = !isOpen(step)
        withAnimation(.easeInOut(duration: 0.18)) { toggled[Self.id(step)] = open }
        if open { prepare(step) }
    }

    func load() {
        generation += 1
        let mine = generation
        Task {
            let result = await cli.background { $0.setup() }
            guard mine == generation else { return }
            switch result {
            case .success(let output):
                steps = output.steps
                done = output.done
                remaining = output.remaining
                loadError = nil
                // Reviews are read again: one taken before a sign-in is out of date.
                reviews = [:]
                for step in output.steps where isOpen(step) { prepare(step) }
                for step in output.steps where step.auto == true && !step.done && !autoStarted.contains(Self.id(step)) {
                    autoStarted.insert(Self.id(step))
                    act(step)
                }
            case .failure(let failure):
                loadError = failure.message
            }
            loaded = true
        }
    }

    /// Reads what an open step shows: the templates, the backend's choices, a
    /// routine's trust review.
    private func prepare(_ step: SetupStep) {
        switch step.step {
        case .templates: if templates == nil { loadTemplates(step) }
        case .backend: loadBackend(step)
        case .turn_hyphen_on: if let routine = step.subject, reviews[routine] == nil { review(routine, step) }
        case .routine, .sign_hyphen_in, .usage, .mcp: break
        }
    }

    /// Does a step that's one action with no answers to give (an `auto` one).
    private func act(_ step: SetupStep) {
        switch step.step {
        case .mcp: connectClaudeCode(step)
        case .usage: setUpUsage(step)
        case .templates, .backend, .routine, .sign_hyphen_in, .turn_hyphen_on: break
        }
    }

    // MARK: Templates

    func loadTemplates(_ step: SetupStep) {
        Task {
            switch await cli.background({ $0.templates() }) {
            case .success(let list): templates = list
            case .failure(let failure): errors[Self.id(step)] = failure.message
            }
        }
    }

    /// The projects templates are for, in the order they're listed.
    var allProjects: [String] {
        var seen: [String] = []
        for project in (templates ?? []).compactMap(\.project) where !project.isEmpty && !seen.contains(project) {
            seen.append(project)
        }
        return seen
    }

    /// The templates offered: those for any project, and the chosen projects'.
    var offered: [TemplateInfo] {
        (templates ?? []).filter { ($0.project ?? "").isEmpty || projects.contains($0.project ?? "") }
    }

    func pickProject(_ project: String, _ on: Bool) {
        if on { projects.insert(project) } else { projects.remove(project) }
    }

    func choose(_ id: String, _ on: Bool) {
        if on { chosen.insert(id) } else { chosen.remove(id) }
    }

    func value(_ template: String, _ param: String) -> Binding<String> {
        Binding(
            get: { self.values[template]?[param] ?? "" },
            set: { self.values[template, default: [:]][param] = $0 })
    }

    /// Installs the chosen templates, each with the values typed for it (empty
    /// ones left to the template's defaults); the core says what's missing.
    func install(_ step: SetupStep) {
        let picked = offered.map(\.id).filter(chosen.contains)
        let typed = picked.map { id in
            (id, (values[id] ?? [:]).filter { !$0.value.isEmpty }.sorted { $0.key < $1.key }.map { ($0.key, $0.value) })
        }
        run(step) { cli in
            var failures: [String] = []
            for (id, values) in typed {
                if case .failure(let failure) = cli.installTemplate(id, values: values) {
                    failures.append("\(id): \(failure.message)")
                }
            }
            return failures.isEmpty ? nil : CLI.Failure(message: failures.joined(separator: "\n"))
        } then: {
            self.chosen = []
            self.loadTemplates(step)
        }
    }

    // MARK: Backend

    func loadBackend(_ step: SetupStep) {
        Task {
            switch await cli.background({ $0.appSettings() }) {
            case .success(let list):
                backend = list.first { $0.key == step.setting }
                backendSettings = (step.settings ?? []).compactMap { key in list.first { $0.key == key } }
                if backend == nil { errors[Self.id(step)] = "The core lists no setting for this step." }
            case .failure(let failure):
                errors[Self.id(step)] = failure.message
            }
        }
    }

    func setBackend(_ step: SetupStep, _ value: String) {
        guard let key = backend?.key else { return }
        run(step) { cli in
            if case .failure(let failure) = cli.setSetting(key, value) { return failure }
            return nil
        } then: {
            self.loadBackend(step)
        }
    }

    /// Sets (or, with nil, resets) one of the backend's own settings.
    func setBackendSetting(_ step: SetupStep, _ key: String, _ value: String?) {
        run(step) { cli in
            let result = value.map { cli.setSetting(key, $0) } ?? cli.resetSetting(key)
            if case .failure(let failure) = result { return failure }
            return nil
        } then: {
            self.loadBackend(step)
        }
    }

    // MARK: Sign-in, Claude Code, turning routines on

    /// Signs in where the step says (a server, or the Anthropic API key):
    /// OAuth in the browser, keys on a local page there.
    func signIn(_ step: SetupStep) {
        guard let server = step.signIn else { return }
        run(step) { $0.signIn(server) }
    }

    func review(_ routine: String, _ step: SetupStep) {
        reviewErrors[routine] = nil
        Task {
            switch await cli.background({ $0.trustCheck(routine) }) {
            case .success(let review): reviews[routine] = review
            case .failure(let failure): reviewErrors[routine] = failure.message
            }
        }
    }

    /// Signs in where a turn-on review says one is still needed, then reads
    /// the steps (and their reviews) again.
    func signIn(named name: String) {
        signingIn = name
        Task {
            let failure = await cli.background { $0.signIn(name) }
            signingIn = nil
            signInError = failure?.message
            if failure == nil { changed() }
            load()
        }
    }

    /// Trusts the routine with what it can reach and lets it run on its
    /// schedule, in one step (after its review).
    func turnOn(_ step: SetupStep) {
        guard let routine = step.subject else { return }
        run(step) { cli in
            if case .failure(let failure) = cli.turnOn(routine) { return failure }
            return nil
        }
    }

    /// Registers the core's MCP server with Claude Code.
    func connectClaudeCode(_ step: SetupStep) {
        run(step) { cli in
            if case .failure(let failure) = cli.connectClaudeCode() { return failure }
            return nil
        }
    }

    /// Has the core record the plan's usage from Claude Code's status line
    /// (it wraps the user's own status line, which still shows the same).
    func setUpUsage(_ step: SetupStep) {
        run(step) { cli in
            if case .failure(let failure) = cli.installStatusLine() { return failure }
            return nil
        }
    }

    /// Does a step's action through the CLI, shows the core's message if it
    /// fails, then reads the steps again.
    private func run(
        _ step: SetupStep, _ work: @escaping @Sendable (CLI) -> CLI.Failure?, then: @escaping () -> Void = {}
    ) {
        let id = Self.id(step)
        working.insert(id)
        Task {
            let failure = await cli.background(work)
            working.remove(id)
            errors[id] = failure?.message
            if failure == nil { changed() }
            then()
            load()
        }
    }
}
