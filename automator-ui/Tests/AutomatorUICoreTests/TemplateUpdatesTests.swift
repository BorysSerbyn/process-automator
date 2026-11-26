import Foundation
import Testing

@testable import AutomatorUICore

/// `templates outdated --json`: standup edited, digest maybe, a hook with
/// its watcher, and one that can't be overridden.
private let outdated = """
    [{"name":"standup","kind":"routine","template":"standup","installedVersion":"1.0","version":"1.2","changes":"Posts to the thread","edited":"yes"},
     {"name":"digest","kind":"routine","template":"digest","version":"2","edited":"unknown"},
     {"name":"open-meet","kind":"hook","template":"open-meet","installedVersion":"1","version":"2","edited":"no","with":["cal"]},
     {"name":"sync","kind":"program","template":"sync","edited":"no","blocked":"its template has no program now"}]
    """

private let calRow = #"{"name":"cal","kind":"watcher","template":"cal","edited":"no"}"#

/// A stand-in for the core's CLI answering the templates commands: it logs
/// each command; `outdated` answers what's left (overriding fails with the
/// core's error object when told to, after which only digest is left).
private func fakeCLI(failOverride: Bool = false, withWatcher: Bool = false) throws -> (CLI, URL) {
    let dir = FileManager.default.temporaryDirectory.appending(path: "automator-ui-test-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let log = dir.appending(path: "log")
    let script = dir.appending(path: "procauto")
    let failed = dir.appending(path: "failed")
    let override =
        failOverride
        ? #"touch '\#(failed.path)'; echo '{"error":"building sync: go not found"}'; exit 1"#
        : #"echo '{"overridden":[{"name":"standup","kind":"routine","version":"1.2","trusted":true},{"name":"open-meet","kind":"hook","version":"2","trusted":false}]}'"#
    try """
    #!/bin/sh
    echo "$*" >> '\(log.path)'
    case "$1 $2" in
    "templates outdated")
        if [ -e '\(failed.path)' ]; then
            echo '[{"name":"digest","kind":"routine","template":"digest","edited":"unknown"}]'
        else
            cat <<'EOF'
    \(withWatcher ? String(outdated.dropLast()) + ",\n " + calRow + "]" : outdated)
    EOF
        fi ;;
    "templates override") \(override) ;;
    "templates skip") echo '{"skipped":[]}' ;;
    esac
    """.write(to: script, atomically: true, encoding: .utf8)
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: script.path)
    return (CLI(executable: script), log)
}

private func ran(_ log: URL) -> [String] {
    ((try? String(contentsOf: log, encoding: .utf8)) ?? "").split(separator: "\n").map(String.init)
}

@MainActor
private func loaded(failOverride: Bool = false, withWatcher: Bool = false) async throws -> (TemplateUpdates, URL) {
    let (cli, log) = try fakeCLI(failOverride: failOverride, withWatcher: withWatcher)
    let model = TemplateUpdates(cli: cli)
    await model.load()
    return (model, log)
}

// None is ticked at first: nothing to override until one is.
@MainActor
@Test func templateUpdatesStartUnticked() async throws {
    let (model, _) = try await loaded()
    #expect(model.updates.map(\.name) == ["standup", "digest", "open-meet", "sync"])
    #expect(model.ticked.isEmpty && !model.canOverride && model.undecided)
    #expect(model.updates[2].with == ["cal"])
    #expect(model.updates[3].blockedReason == "its template has no program now")
}

// Select All ticks every row but a blocked one, which can't be ticked.
@MainActor
@Test func selectAllSkipsBlockedRows() async throws {
    let (model, _) = try await loaded()
    model.selectAll()
    #expect(model.ticked == ["standup", "digest", "open-meet"])
    model.setTicked("sync", true)
    #expect(!model.isTicked("sync"))
    model.setTicked("digest", false)
    #expect(model.toOverride == ["standup", "open-meet"] && model.toSkip == ["digest"] && model.canOverride)
}

// Override replaces the ticked ones, then skips the others that could have
// been: never the blocked one.
@MainActor
@Test func overrideReplacesTheTickedAndSkipsTheRest() async throws {
    let (model, log) = try await loaded()
    model.setTicked("standup", true)
    model.setTicked("open-meet", true)
    await model.override()
    #expect(
        ran(log) == [
            "templates outdated --json", "templates override standup open-meet --json",
            "templates skip digest --json",
        ])
    #expect(model.phase == .done && model.error == nil && !model.undecided)
    #expect(model.overridden.map(\.name) == ["standup", "open-meet"])
    #expect(model.overridden.map(\.trusted) == [true, false])
}

// Overriding fails: the error, the rows left, nothing skipped.
@MainActor
@Test func failedOverrideShowsWhatsLeft() async throws {
    let (model, log) = try await loaded(failOverride: true)
    model.selectAll()
    await model.override()
    #expect(model.error == "building sync: go not found")
    #expect(model.phase == .choosing && model.updates.map(\.name) == ["digest"])
    #expect(model.ticked == ["digest"])
    #expect(!ran(log).contains { $0.hasPrefix("templates skip") })
}

// Not now skips every row listed, blocked ones too; once chosen, leaving
// skips nothing more.
@MainActor
@Test func notNowSkipsThemAll() async throws {
    let (model, log) = try await loaded()
    model.setTicked("standup", true)
    #expect(await model.notNow())
    #expect(model.phase == .done && !model.undecided)
    #expect(await model.notNow())
    #expect(ran(log) == ["templates outdated --json", "templates skip standup digest open-meet sync --json"])
}

@Test func templateUpdateVersionsOmitWhatsMissing() throws {
    let updates = try JSONDecoder().decode([TemplateUpdate].self, from: Data(outdated.utf8))
    #expect(updates.map(\.versions) == ["1.0 → 1.2", "→ 2", "1 → 2", nil])
}

@Test func templateUpdatesOpenOverUpdates() {
    var nav = NavigationState()
    nav.show(.templateUpdates)
    #expect(nav.section == .updates && nav.path == [.templateUpdates] && nav.backTitle == "Updates")
    #expect(Destination.templateUpdates.title == "Template Updates" && !Destination.templateUpdates.isRoot)
    nav.removed(.templateUpdates)
    #expect(nav.shown == .updates)
}

// A ticked hook's watcher is overridden with it, so it isn't skipped.
@MainActor
@Test func tickedHookKeepsItsWatcher() async throws {
    let (model, _) = try await loaded(withWatcher: true)
    model.setTicked("open-meet", true)
    #expect(!model.toSkip.contains("cal"))
    #expect(model.toSkip == ["standup", "digest"])
    model.setTicked("open-meet", false)
    #expect(model.toSkip.contains("cal"))
}
