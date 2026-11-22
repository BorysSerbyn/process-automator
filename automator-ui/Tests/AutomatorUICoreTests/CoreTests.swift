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

@Test func decodesRoutinesOfBothKinds() throws {
    // `routines list --kind all`: an LLM routine has no kind; a program has kind program and its section.
    let json = """
        [{"name":"standup","path":"/r/standup.prompt","servers":["chat"],"tools":["post"],"enabled":true,
          "timeout":"20m","effort":"default","session":"discard","builtinTools":[],"trusted":true},
         {"name":"backup","kind":"program","path":"/p/backup/program.yaml","servers":[],"tools":[],"enabled":false,
          "timeout":"20m","effort":"","session":"","builtinTools":[],"trusted":false,
          "program":{"executable":"/p/backup/backup","checksum":"9f86","args":[],"writes":true,
            "secrets":[{"name":"api-token","env":"API_TOKEN","stored":false}]}}]
        """
    let rows = try CoreJSON.decoder().decode([Components.Schemas.RoutineInfo].self, from: Data(json.utf8))
    #expect(rows[0].kind == nil)
    #expect(rows[0].program == nil)
    #expect(rows[1].kind == .program)
    #expect(rows[1].program?.writes == true)
    #expect(rows[1].program?.secrets.first?.env == "API_TOKEN")
}

@Test func decodesAProgramsDetailAndRun() throws {
    // `routines show <program>`: its description file's text, description and data folder.
    let detail = """
        {"name":"backup","kind":"program","path":"/p/backup/program.yaml","description":"Backs up my notes",
         "servers":[],"tools":[],"enabled":true,"timeout":"20m","effort":"","session":"","builtinTools":[],
         "folder":"/d/backup","trusted":true,"source":"executable: ./backup\\nwrites: true\\n",
         "program":{"executable":"/p/backup/backup","checksum":"9f86d081884c7d659a2feaa0c55ad015","args":["--quiet"],
           "writes":true,"secrets":[{"name":"api-token","env":"API_TOKEN","stored":true}]}}
        """
    let d = try CoreJSON.decoder().decode(Components.Schemas.RoutineDetail.self, from: Data(detail.utf8))
    #expect(d.kind == .program)
    #expect(d.description == "Backs up my notes")
    #expect(d.folder == "/d/backup")
    #expect(d.source.hasPrefix("executable: ./backup"))
    // `runs show <id>`: its exit code, output (cut down, and saying so) and when it finished.
    let run = """
        {"id":"backup/2026-11-21T090000","routine":"backup","kind":"program","trigger":"manual",
         "startedAt":"2026-11-21T09:00:00Z","finishedAt":"2026-11-21T09:00:04Z","outcome":"failed","reason":"exit 2",
         "program":{"exitCode":2,"output":"start\\n[… 40 KiB of output left out …]\\nend","outputTruncated":true,
           "dryRun":true,"checksum":"9f86"}}
        """
    let e = try CoreJSON.decoder().decode(Components.Schemas.Entry.self, from: Data(run.utf8))
    #expect(e.result == nil)
    #expect(e.program?.exitCode == 2)
    #expect(e.program?.dryRun == true)
    #expect(e.program?.truncatedText != nil)
    #expect(e.finishedAt == CoreJSON.parseDate("2026-11-21T09:00:04Z"))
}

@Test func putsAProgramsFactsInWords() {
    typealias Program = Components.Schemas.ProgramInfo
    typealias Secret = Components.Schemas.ProgramSecret
    let writes = Program(
        args: [], checksum: "9f86d081884c7d659a2f", executable: "/p/b",
        secrets: [Secret(env: "A", name: "a", stored: true), Secret(env: "B", name: "b", stored: false)], writes: true)
    #expect(writes.dryRunText.contains("--dry-run"))
    #expect(writes.shortChecksum == "9f86d081884c…")
    #expect(writes.needsSignIn)
    let reads = Program(args: [], checksum: "", executable: "/p/b", secrets: [], writes: false)
    #expect(reads.dryRunText == "It doesn't write: Dry Run runs it as it is.")
    #expect(reads.shortChecksum == nil)
    #expect(!reads.needsSignIn)
    #expect(Components.Schemas.Execution(dryRun: false, exitCode: 0, output: "ok").truncatedText == nil)
}

@Test func signingInToAProgramRunsAuthLogin() throws {
    // A program's window signs in to its secrets as setup's sign-in step does.
    let (cli, log) = try fakeCLI()
    #expect(cli.signIn("backup") == nil)
    #expect(ran(log) == ["auth login backup --browser"])
}

// MARK: Hooks and watchers

@Test func runStatusKeepsHooksApartFromRoutines() throws {
    // `status --watch` with hooks: their own array, and reasons about one say so.
    let json = """
        {"schemaVersion":1,"updatedAt":"2026-11-22T14:55:10Z","health":"error","busy":false,
         "routines":[{"name":"standup","enabled":true,"health":"ok","summary":"Runs by hand"}],
         "hooks":[{"name":"brief","enabled":false,"health":"none","summary":"Paused"},
                  {"name":"open-meet","enabled":true,"problem":"its watcher cal is paused, so it hears nothing",
                   "health":"error","summary":"open-meet can't run"}],
         "reasons":[{"routine":"open-meet","hook":true,"text":"open-meet can't run",
                     "detail":"its watcher cal is paused, so it hears nothing"}]}
        """
    let status = try RunStatus.decode(Data(json.utf8))
    #expect(status.routines.map(\.name) == ["standup"])
    #expect(status.hook("open-meet")?.health == "error")
    #expect(status.hook("standup") == nil)
    #expect(status.reasons?.first?.hook == true)
    // No hooks: the array is absent.
    let none = try RunStatus.decode(
        Data(#"{"schemaVersion":1,"updatedAt":"2026-11-22T14:55:10Z","health":"ok","busy":false,"routines":[]}"#.utf8))
    #expect(none.hooks == nil && none.hook("brief") == nil)
    // A notification about a hook's run.
    let click = try CoreJSON.decoder().decode(
        Components.Schemas.ClickOutput.self,
        from: Data(
            #"{"notification":"n1","action":"show-run","routine":"open-meet","run":"open-meet/x","hook":true}"#.utf8))
    #expect(click.hook == true && click.routine == "open-meet")
}

@Test func decodesHooksAndTheirWatcher() throws {
    // `hooks list`: an LLM hook has no kind; a program hook has kind program.
    let list = """
        [{"name":"brief","path":"/h/brief/hook.prompt","description":"Briefs me",
          "on":{"watcher":"cal","type":"meeting.started"},"batch":true,"enabled":false,"staleness":"10m",
          "timeout":"20m","rate":60,"trusted":false,"untrusted":"not trusted yet","builtinTools":["Bash"]},
         {"name":"open-meet","kind":"program","path":"/h/open-meet/hook.yaml","on":{"watcher":"cal"},
          "enabled":true,"staleness":"2m","timeout":"20m","rate":20,"trusted":true,
          "program":{"executable":"/h/open-meet/act","checksum":"6666","args":[],"writes":true,"secrets":[]}}]
        """
    let hooks = try CoreJSON.decoder().decode([Components.Schemas.HookInfo].self, from: Data(list.utf8))
    #expect(!hooks[0].isProgram && hooks[0].batch == true && hooks[0].builtinTools == ["Bash"])
    #expect(hooks[1].isProgram && hooks[1].program?.writes == true)
    #expect(hooks[0].on.text == "cal's meeting.started events")
    #expect(hooks[1].on.text == "every event from cal")
    // `hooks show`: its file and its watcher.
    let show = """
        {"name":"open-meet","kind":"program","path":"/h/open-meet/hook.yaml","on":{"watcher":"cal","type":"meeting.started"},
         "enabled":true,"staleness":"2m","timeout":"20m","rate":20,"trusted":true,
         "problem":"its watcher cal is paused, so it hears nothing","source":"executable: ./act\\n",
         "watcher":{"name":"cal","path":"/w/cal/watcher.yaml","mode":"poll","interval":"30s","timeout":"30s",
           "executable":"/w/cal/watch","checksum":"9ab8","args":[],"secrets":[{"name":"cal-token","env":"CAL_TOKEN","stored":false}],
           "enabled":false,"trusted":true,"hooks":["brief","open-meet"],"error":"exit 1","errorAt":"2026-11-22T09:00:00Z"}}
        """
    let detail = try CoreJSON.decoder().decode(Components.Schemas.HookDetail.self, from: Data(show.utf8))
    #expect(detail.isProgram && detail.watcher?.enabled == false && detail.watcher?.hooks.count == 2)
    #expect(detail.watcher?.modeText == "Polled every 30s")
    #expect(detail.watcher?.errorAt == CoreJSON.parseDate("2026-11-22T09:00:00Z"))
    // `hooks trust --check`: what it acts on, its watcher's trust, and warnings.
    let review = """
        {"routine":"brief","surface":{"servers":[],"tools":[],"builtinTools":["Bash"]},"changes":[],"trusted":false,
         "hook":true,"trigger":{"watcher":"cal","type":"meeting.started","batch":true},
         "watcher":{"name":"cal","trusted":false,"untrusted":"not trusted yet"},
         "warnings":["it can write (Bash) and acts on outside event data"]}
        """
    let trust = try CoreJSON.decoder().decode(Components.Schemas.TrustOutput.self, from: Data(review.utf8))
    #expect(trust.hook == true && trust.trigger?.batch == true && trust.watcher?.trusted == false)
    #expect(trust.warnings?.count == 1)
}

@Test func decodesWatchers() throws {
    let show = """
        {"name":"inbox","path":"/w/inbox/watcher.yaml","description":"New mail","mode":"stream","executable":"/w/inbox/watch",
         "checksum":"9ab8fabc8c72bfa0","args":["--all"],"secrets":[{"name":"mail","env":"MAIL","stored":false}],
         "enabled":true,"trusted":false,"untrusted":"not trusted yet","hooks":[],"startedAt":"2026-11-22T08:00:00Z",
         "source":"mode: stream\\n"}
        """
    let w = try CoreJSON.decoder().decode(Components.Schemas.WatcherDetail.self, from: Data(show.utf8))
    #expect(w.modeText == "Streams events")
    #expect(w.needsSignIn)
    #expect(w.program.executable == "/w/inbox/watch" && w.program.args == ["--all"] && !w.program.writes)
    let review = """
        {"watcher":"inbox","program":{"executable":"/w/inbox/watch","checksum":"9ab8","args":[],"writes":false,"secrets":[]},
         "mode":"stream","changes":["its executable changed"],"trusted":false}
        """
    let trust = try CoreJSON.decoder().decode(Components.Schemas.WatcherTrustOutput.self, from: Data(review.utf8))
    #expect(trust.changes == ["its executable changed"] && trust.mode == .stream)
}

@Test func aHooksRunKeepsItsEventsAsPlainText() throws {
    // `runs show` of a hook's run: the events it ran for. Their data is
    // outside data: kept as text, markup and all.
    let run = """
        {"id":"open-meet/2026-11-22T093000","routine":"open-meet","hook":true,"kind":"program","trigger":"event",
         "startedAt":"2026-11-22T09:30:00Z","outcome":"success",
         "events":[{"id":"m1","type":"meeting.started","at":"2026-11-22T09:30:00Z","watcher":"cal",
                    "data":{"title":"**Standup** <b>now</b>","link":"https://meet.google.com/abc"}},
                   {"id":"m2","type":"meeting.started","at":"2026-11-22T09:31:00Z"}]}
        """
    let e = try CoreJSON.decoder().decode(Components.Schemas.Entry.self, from: Data(run.utf8))
    #expect(e.hook == true && e.trigger == "event")
    let events = try #require(e.events)
    #expect(events[0].dataText.contains(#""title" : "**Standup** <b>now</b>""#))
    #expect(events[0].dataText.contains("https://meet.google.com/abc"))
    #expect(events[1].dataText.isEmpty)
}

@Test func putsAHooksFactsInWords() throws {
    #expect(HookWords.staleness("2m") == "Events older than 2m are dropped.")
    #expect(HookWords.rate(20) == "At most 20 runs an hour; far past it, it's paused.")
    #expect(HookWords.rate(1).hasPrefix("At most 1 run an hour"))
    #expect(HookWords.kind(program: true) == "Program" && HookWords.kind(program: false) == "Prompt")
    #expect(HookWords.dryRun(program: true).contains("needs trusting even for a dry run"))
    #expect(HookWords.dryRun(program: false).contains("needs no trust"))
    // The sample event is JSON, of the type it acts on.
    let sample = HookWords.sampleEvent(.init(_type: "meeting.started", watcher: "cal"))
    let object = try #require(try JSONSerialization.jsonObject(with: Data(sample.utf8)) as? [String: Any])
    #expect(object["type"] as? String == "meeting.started")
    #expect(object["id"] as? String != nil)
}

/// A stand-in CLI that logs what it's given and prints `reply`.
private func loggingCLI(reply: String) throws -> (CLI, URL) {
    let dir = FileManager.default.temporaryDirectory.appending(path: "automator-ui-test-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let log = dir.appending(path: "log")
    let script = dir.appending(path: "procauto")
    try """
    #!/bin/sh
    echo "$*" >> '\(log.path)'
    echo '\(reply)'
    """.write(to: script, atomically: true, encoding: .utf8)
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: script.path)
    return (CLI(executable: script), log)
}

@Test func hooksAndWatchersRunTheirOwnCommands() throws {
    let (cli, log) = try loggingCLI(reply: "{}")
    _ = cli.hooks()
    _ = cli.hook("open-meet")
    _ = cli.hookTrustCheck("open-meet")
    _ = cli.trustHook("open-meet", turnOn: false)
    _ = cli.trustHook("open-meet", turnOn: true)
    _ = cli.setHookPaused("open-meet", true)
    _ = cli.setHookPaused("open-meet", false)
    _ = cli.deleteHook("open-meet")
    _ = cli.fireHook("open-meet", event: #"{"id":"1","type":"meeting.started"}"#, dryRun: true)
    _ = cli.fireHook("open-meet", event: #"{"id":"1","type":"meeting.started"}"#, dryRun: false)
    _ = cli.watchers()
    _ = cli.watcher("cal")
    _ = cli.watcherTrustCheck("cal")
    _ = cli.trustWatcher("cal")
    _ = cli.setWatcherPaused("cal", true)
    _ = cli.setWatcherPaused("cal", false)
    _ = cli.deleteWatcher("cal")
    #expect(cli.signIn("cal") == nil)
    #expect(
        ran(log) == [
            "hooks list --json", "hooks show open-meet --json", "hooks trust open-meet --check --json",
            "hooks trust open-meet --yes --json", "hooks trust open-meet --yes --resume --json",
            "hooks pause open-meet --json", "hooks resume open-meet --json", "hooks delete open-meet --json",
            #"hooks fire open-meet --event-json {"id":"1","type":"meeting.started"} --dry-run --json"#,
            #"hooks fire open-meet --event-json {"id":"1","type":"meeting.started"} --json"#,
            "watchers list --json", "watchers show cal --json", "watchers trust cal --check --json",
            "watchers trust cal --yes --json", "watchers pause cal --json", "watchers resume cal --json",
            "watchers delete cal --json", "auth login cal --browser",
        ])
}

@Test func firingAHookSaysHowItWent() throws {
    let (cli, _) = try loggingCLI(reply: #"{"kind":"program","program":{"exitCode":0,"output":"ok","dryRun":true}}"#)
    let fired = try cli.fireHook("open-meet", event: "{}", dryRun: true).get()
    #expect(fired.kind == .program && fired.program?.dryRun == true)
}
