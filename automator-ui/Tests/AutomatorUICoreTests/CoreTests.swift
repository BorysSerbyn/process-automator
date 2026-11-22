import Foundation
import Testing

@testable import AutomatorUICore

@Test func decodesSharedContractExample() throws {
    // Same example file the Go tests use, read straight from the repo.
    let url = URL(filePath: #filePath)
        .deletingLastPathComponent()
        .appending(path: "../../../contracts/examples/run-status.json")
    let status = try RunStatus.decode(Data(contentsOf: url))
    #expect(status.schemaVersion == RunStatus.supportedSchemaVersion)
    #expect(status.health == .error)
    #expect(status.validUntil != nil)
}

@Test func decodesWhatTheCoreWrites() throws {
    // Shaped like the core's file: fractional seconds, routines with runs and next slots.
    let json = """
        {"schemaVersion":1,"updatedAt":"2026-11-18T11:28:03.713458-05:00",
         "validUntil":"2026-11-18T11:29:03.713458-05:00","health":"ok","busy":false,
         "routines":[{"name":"standup","enabled":true,"nextRunAt":"2026-11-19T09:20:00-05:00",
           "lastRun":{"startedAt":"2026-11-18T09:20:01.5Z","outcome":"success"}}]}
        """
    let status = try RunStatus.decode(Data(json.utf8))
    #expect(status.health == .ok)
    #expect(status.validUntil == CoreJSON.parseDate("2026-11-18T16:29:03.713458Z"))
    #expect(status.routines.first?.lastRun?.outcome == .success)
}

@Test func lightShowsTheCoresVerdictWhileItResponds() throws {
    func status(_ health: String, validUntil: String? = "2026-11-18T09:31:00Z") throws -> RunStatus {
        let valid = validUntil.map { #""validUntil":"\#($0)","# } ?? ""
        return try RunStatus.decode(
            Data(
                """
                {"schemaVersion":1,"updatedAt":"2026-11-18T09:30:00Z",\(valid)"health":"\(health)","busy":false,"routines":[]}
                """.utf8))
    }
    let now = try #require(CoreJSON.parseDate("2026-11-18T09:30:30Z"))
    #expect(RunStatus.light(for: try status("ok"), now: now) == .green)
    #expect(RunStatus.light(for: try status("error"), now: now) == .red)
    #expect(RunStatus.light(for: try status("none"), now: now) == .grey)
    // The core isn't responding: no status, an expired one, or none at all.
    #expect(RunStatus.light(for: nil, now: now) == .red)
    #expect(RunStatus.light(for: try status("ok", validUntil: "2026-11-18T09:30:00Z"), now: now) == .red)
    #expect(RunStatus.light(for: try status("ok", validUntil: nil), now: now) == .red)
}

@Test func unsupportedSchemaIsUnknown() {
    let line = Data(#"{"schemaVersion":99,"updatedAt":"2026-11-18T09:31:00Z","health":"ok","routines":[]}"#.utf8)
    #expect(StatusStream.status(from: line) == nil)
    #expect(StatusStream.status(from: Data("not json".utf8)) == nil)
}

@Test func cliRunsCommandsAndReportsFailure() throws {
    let echo = CLI(executable: URL(filePath: "/bin/echo")).exec(["hi"])
    #expect(echo.ok && String(decoding: echo.stdout, as: UTF8.self) == "hi\n")
    #expect(!CLI(executable: URL(filePath: "/usr/bin/false")).exec([]).ok)
    #expect(!CLI(executable: URL(filePath: "/nonexistent")).exec([]).ok)
    // stderr is captured alongside stdout.
    let sh = CLI(executable: URL(filePath: "/bin/sh")).exec([
        "-c", "echo out; echo 'Error: no such routine' >&2; exit 1",
    ])
    #expect(!sh.ok && sh.failure.message == "no such routine")
}

@Test func cliStopsACommandThatHangs() {
    var cli = CLI(executable: URL(filePath: "/bin/sleep"))
    cli.timeout = 0.5
    let started = Date()
    let hung = cli.exec(["30"])
    #expect(!hung.ok && hung.failure.message.hasSuffix(" 30 didn't answer in 1 second"))
    #expect(Date().timeIntervalSince(started) < 10)
    // .infinity waits for it; a quick one is unaffected by the limit.
    #expect(cli.exec(["0.1"], timeout: .infinity).ok)
    #expect(CLI.duration(120) == "2 minutes" && CLI.duration(30) == "30 seconds")
}

@Test func failureIsTheCoresErrorObject() {
    func output(_ stdout: String, _ stderr: String) -> CLI.Output {
        CLI.Output(stdout: Data(stdout.utf8), stderr: stderr, ok: false)
    }
    // --json: the error object, whole, however many lines it has.
    let multiline = "ui: launchctl failed\nscheduler: launchctl failed"
    let json = output(#"{"error":"ui: launchctl failed\nscheduler: launchctl failed"}"#, "Error: ui: …")
    #expect(json.failure.message == multiline)
    // Otherwise what it wrote to stderr, all of it.
    #expect(output("", "Error: \(multiline)\n").failure.message == multiline)
    #expect(output("", "panic: boom").failure.message == "panic: boom")
    #expect(output("", "").failure.message == "procauto failed")
}

@Test func decodesReasonsAndSummary() throws {
    let json = """
        {"schemaVersion":1,"updatedAt":"2026-11-18T09:30:00Z","health":"error","busy":false,"routines":[],
         "reasons":[{"routine":"standup","text":"standup failed","detail":"timed out","run":"standup/x"},
                    {"routine":"a","text":"a is stuck"}]}
        """
    let status = try RunStatus.decode(Data(json.utf8))
    #expect(status.reasons?.map(\.text) == ["standup failed", "a is stuck"])
    #expect(status.reasons?.last?.run == nil)
    let ok = try RunStatus.decode(
        Data(
            #"{"schemaVersion":1,"updatedAt":"2026-11-18T09:30:00Z","health":"ok","busy":false,"routines":[],"summary":"All good"}"#
                .utf8))
    #expect(ok.summary == "All good")
}

@Test func pulsesOnlyWhileTheCoreSaysBusyAndResponds() throws {
    func status(busy: Bool, validUntil: String) throws -> RunStatus {
        try RunStatus.decode(
            Data(
                """
                {"schemaVersion":1,"updatedAt":"2026-11-18T09:30:00Z","validUntil":"\(validUntil)",
                 "health":"ok","busy":\(busy),"routines":[]}
                """.utf8))
    }
    let now = try #require(CoreJSON.parseDate("2026-11-18T09:30:30Z"))
    #expect(RunStatus.pulsing(for: try status(busy: true, validUntil: "2026-11-18T09:31:00Z"), now: now))
    #expect(!RunStatus.pulsing(for: try status(busy: false, validUntil: "2026-11-18T09:31:00Z"), now: now))
    #expect(!RunStatus.pulsing(for: try status(busy: true, validUntil: "2026-11-18T09:30:00Z"), now: now))
    #expect(!RunStatus.pulsing(for: nil, now: now))
}

@Test func decodesPendingNotifications() throws {
    // `notifications list --pending --json`.
    let json = """
        [{"id":"a","at":"2026-11-19T09:21:03.5-05:00","kind":"failure","title":"standup failed","body":"timed out",
          "routine":"standup","run":"standup/2026-11-19T092001"},
         {"id":"b","at":"2026-11-19T10:00:00Z","kind":"app","title":"Updated to 2.2.0"}]
        """
    let list = try CoreJSON.decoder().decode([CoreNotification].self, from: Data(json.utf8))
    #expect(list.map(\.id) == ["a", "b"])
    #expect(list.last?.kind == .app)
}

@Test func statusStreamSplitsLines() {
    // `status --watch` prints a line per status; reads can split them anywhere.
    let one = #"{"schemaVersion":1,"updatedAt":"2026-11-18T09:30:00Z","health":"ok","busy":false,"routines":[]}"#
    var buffer = Data((one + "\n" + one.prefix(20)).utf8)
    let lines = StatusStream.lines(from: &buffer)
    #expect(lines.count == 1)
    #expect(StatusStream.status(from: lines[0])?.health == .ok)
    buffer.append(Data((one.dropFirst(20) + "\n").utf8))
    #expect(StatusStream.lines(from: &buffer).count == 1)
    #expect(buffer.isEmpty)
}

@Test func groupsSettingsTwoLevelsDeep() {
    let keys = [
        "llm.backend", "llm.budget.window", "history.retention-days", "llm.budget.weekly-usd", "llm.usage-limit",
    ]
    let tree = KeyTree.build(keys) { $0 }
    #expect(tree.map(\.name) == ["llm", "history"])
    #expect(tree[0].entries.map(\.label) == ["backend", "usage-limit"])
    #expect(tree[0].groups.map(\.id) == ["llm.budget"])
    #expect(tree[0].groups[0].entries.map(\.label) == ["window", "weekly-usd"])
    #expect(tree[1].entries.map(\.item) == ["history.retention-days"])
}

/// A stand-in for the core's CLI: a script that logs each command it's
/// given, and fails `login-items stop` when told to, with the core's error object.
private func fakeCLI(failStop: Bool = false) throws -> (CLI, URL) {
    let dir = FileManager.default.temporaryDirectory.appending(path: "automator-ui-test-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let log = dir.appending(path: "log")
    let script = dir.appending(path: "procauto")
    let fail =
        failStop
        ? #"[ "$*" = "login-items stop --json" ] && { echo '{"error":"launchctl bootout failed"}'; exit 1; }"# : ""
    try """
    #!/bin/sh
    echo "$*" >> '\(log.path)'
    \(fail)
    echo '{"scheduler":"running","changed":true}'
    """.write(to: script, atomically: true, encoding: .utf8)
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: script.path)
    return (CLI(executable: script), log)
}

private func ran(_ log: URL) -> [String] {
    ((try? String(contentsOf: log, encoding: .utf8)) ?? "").split(separator: "\n").map(String.init)
}

@Test func reopeningShowsSetupWhileSetupIsLeftElseSettings() throws {
    func status(_ setupLeft: String) throws -> RunStatus {
        try RunStatus.decode(
            Data(
                """
                {"schemaVersion":1,"updatedAt":"2026-11-18T09:30:00Z","health":"ok","busy":false,"routines":[]\(setupLeft)}
                """.utf8))
    }
    #expect(Lifecycle.reopenWindow(for: try status(#","setupLeft":["backend"]"#)) == .setup)
    #expect(Lifecycle.reopenWindow(for: try status(#","setupLeft":[]"#)) == .settings)
    #expect(Lifecycle.reopenWindow(for: try status("")) == .settings)
    #expect(Lifecycle.reopenWindow(for: nil) == .settings)
}

@Test func openingTheAppStartsTheScheduler() async throws {
    let (cli, log) = try fakeCLI()
    #expect(await Lifecycle.opened(cli) == nil)
    #expect(ran(log) == ["login-items start --json"])
}

@MainActor
@Test func quitStopsTheSchedulerThenTerminates() async throws {
    var events: [String] = []
    let (cli, log) = try fakeCLI()
    await Lifecycle.quit(
        cli, failed: { events.append("failed: \($0.message)") }, terminate: { events.append("terminate") })
    #expect(ran(log) == ["login-items stop --json"])
    #expect(events == ["terminate"])
    // Stopping fails: why, then it terminates anyway.
    events = []
    let (failing, _) = try fakeCLI(failStop: true)
    await Lifecycle.quit(
        failing, failed: { events.append("failed: \($0.message)") }, terminate: { events.append("terminate") })
    #expect(events == ["failed: launchctl bootout failed", "terminate"])
}
