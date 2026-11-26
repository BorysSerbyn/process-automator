import Foundation

/// An install (a routine, program, hook or watcher) whose template changed
/// in the version installed: one row of `templates outdated`.
public typealias TemplateUpdate = Components.Schemas.TemplateUpdate

extension TemplateUpdate {
    /// Why it can't be overridden (nil: it can).
    public var blockedReason: String? {
        guard let blocked, !blocked.isEmpty else { return nil }
        return blocked
    }

    /// Its versions: "1.0 → 1.2", "→ 1.2" with no installed version recorded,
    /// "1.0 →" when the template has none now; nil with neither.
    public var versions: String? {
        let from = installedVersion?.isEmpty == false ? installedVersion : nil
        let to = version?.isEmpty == false ? version : nil
        switch (from, to) {
        case (nil, nil): return nil
        case (let from?, nil): return "\(from) →"
        case (nil, let to?): return "→ \(to)"
        case (let from?, let to?): return "\(from) → \(to)"
        }
    }
}

extension CLI {
    /// How long overriding may take: it may build a program from source.
    static let overrideTimeout: TimeInterval = 30 * 60

    /// The installs whose template changed and that weren't skipped
    /// (`templates outdated`); empty when none did. No network.
    public func outdatedTemplates() -> Result<[TemplateUpdate], Failure> {
        call([TemplateUpdate].self, ["templates", "outdated"])
    }

    /// Replaces installs with their template's new version (`templates
    /// override`). On a failure some may have been done already.
    public func overrideTemplates(_ names: [String]) -> Result<Components.Schemas.OverrideOutput, Failure> {
        call(Components.Schemas.OverrideOutput.self, ["templates", "override"] + names, timeout: Self.overrideTimeout)
    }

    /// Doesn't offer these installs' template changes again; a later change
    /// is (`templates skip`).
    public func skipTemplates(_ names: [String]) -> Result<Components.Schemas.SkipOutput, Failure> {
        call(Components.Schemas.SkipOutput.self, ["templates", "skip"] + names)
    }
}

/// The Template Updates screen, opened when the app starts after an update
/// changed templates something was installed from: a row per install, none
/// ticked; Select All ticks every one that can be overridden. Override
/// replaces the ticked ones, then skips the others; Not now (or closing the
/// window without choosing) skips them all, so they aren't offered again.
@MainActor
public final class TemplateUpdates: ObservableObject {
    public enum Phase: Equatable, Sendable {
        /// Reading, or choosing what to override.
        case choosing
        /// Overriding (it can take minutes), then skipping the rest.
        case overriding
        /// Skipping them all (Not now).
        case skipping
        /// Chosen: overridden (what `overridden` lists) or skipped.
        case done
    }

    let cli: CLI
    @Published public private(set) var updates: [TemplateUpdate] = []
    @Published public private(set) var loaded = false
    @Published public private(set) var ticked: Set<String> = []
    @Published public private(set) var phase = Phase.choosing
    /// What the last action did, once it succeeded.
    @Published public private(set) var overridden: [Components.Schemas.Overridden] = []
    /// Why reading, overriding or skipping failed.
    @Published public private(set) var error: String?

    public init(cli: CLI) { self.cli = cli }

    public var busy: Bool { phase == .overriding || phase == .skipping }

    /// Whether there's still a choice to make: leaving now counts as Not now.
    public var undecided: Bool { phase == .choosing && !updates.isEmpty }

    public func load() async {
        let result = await cli.background { $0.outdatedTemplates() }
        switch result {
        case .success(let list):
            updates = list
            let names = Set(selectable.map(\.name))
            ticked.formIntersection(names)
        case .failure(let failure):
            error = failure.message
        }
        loaded = true
    }

    /// The rows that can be overridden.
    public var selectable: [TemplateUpdate] { updates.filter { $0.blockedReason == nil } }

    public func isTicked(_ name: String) -> Bool { ticked.contains(name) }

    /// Ticks or unticks a row; a blocked one stays unticked.
    public func setTicked(_ name: String, _ on: Bool) {
        guard !busy, selectable.contains(where: { $0.name == name }) else { return }
        if on { ticked.insert(name) } else { ticked.remove(name) }
    }

    /// Ticks every row that can be overridden.
    public func selectAll() {
        guard !busy else { return }
        ticked = Set(selectable.map(\.name))
    }

    public var canOverride: Bool { phase == .choosing && !toOverride.isEmpty }

    /// What Override replaces: the ticked rows, in the list's order.
    public var toOverride: [String] { selectable.map(\.name).filter(ticked.contains) }

    /// What Override skips after: the rows not ticked that could have been,
    /// but not a ticked hook's watcher, overridden with it.
    public var toSkip: [String] {
        let with = Set(updates.filter { ticked.contains($0.name) }.flatMap { $0.with ?? [] })
        return selectable.map(\.name).filter { !ticked.contains($0) && !with.contains($0) }
    }

    /// Overrides the ticked rows, then skips the others. When overriding
    /// fails, the list is read again: what's left shows with the error,
    /// nothing skipped.
    public func override() async {
        guard canOverride else { return }
        let names = toOverride
        let skip = toSkip
        phase = .overriding
        error = nil
        switch await cli.background({ $0.overrideTemplates(names) }) {
        case .success(let output):
            overridden = output.overridden
        case .failure(let failure):
            await load()
            error = failure.message
            phase = .choosing
            return
        }
        if !skip.isEmpty, case .failure(let failure) = await cli.background({ $0.skipTemplates(skip) }) {
            error = failure.message
        }
        phase = .done
    }

    /// Skips every row listed (Not now, or leaving the screen without
    /// choosing): whether it succeeded (the error says why not).
    @discardableResult
    public func notNow() async -> Bool {
        guard undecided else { return true }
        let names = updates.map(\.name)
        phase = .skipping
        error = nil
        if case .failure(let failure) = await cli.background({ $0.skipTemplates(names) }) {
            error = failure.message
            phase = .choosing
            return false
        }
        phase = .done
        return true
    }
}
