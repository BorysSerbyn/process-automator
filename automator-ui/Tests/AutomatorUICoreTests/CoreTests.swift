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

@Test func unsupportedSchemaIsUnknown() throws {
    let url = FileManager.default.temporaryDirectory.appending(path: "rs-\(UUID()).json")
    try Data(#"{"schemaVersion":99,"updatedAt":"2026-11-18T09:31:00Z","health":"ok","routines":[]}"#.utf8)
        .write(to: url)
    #expect(RunStatus.load(from: url) == nil)
    #expect(RunStatus.load(from: url.appending(path: "missing")) == nil)
}

@Test func cliRunsCommandsAndReportsFailure() throws {
    let echo = CLI(executable: URL(filePath: "/bin/echo")).exec(["hi"])
    #expect(echo.ok && String(decoding: echo.stdout, as: UTF8.self) == "hi\n")
    #expect(!CLI(executable: URL(filePath: "/usr/bin/false")).exec([]).ok)
    #expect(CLI(executable: URL(filePath: "/nonexistent")).paths() == nil)
    // stderr is captured alongside stdout.
    let sh = CLI(executable: URL(filePath: "/bin/sh")).exec([
        "-c", "echo out; echo 'Error: no such routine' >&2; exit 1",
    ])
    #expect(!sh.ok && sh.failure.message == "no such routine")
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

@Test func decodesNotifications() throws {
    let json = """
        {"schemaVersion":1,"notifications":[
          {"id":"a","at":"2026-11-19T09:21:03.5-05:00","kind":"failure","title":"standup failed","body":"timed out",
           "routine":"standup","run":"standup/2026-11-19T092001","deliveredAt":"2026-11-19T09:21:05Z"},
          {"id":"b","at":"2026-11-19T10:00:00Z","kind":"routine","title":"3 open threads","urgent":true}]}
        """
    let file = try NotificationsFile.decode(Data(json.utf8))
    #expect(file.pending.map(\.id) == ["b"])
    #expect(file.pending.first?.urgent == true)
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
