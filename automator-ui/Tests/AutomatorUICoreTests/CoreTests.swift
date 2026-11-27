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

@Test func eachReasonOpensWhatItsAbout() throws {
    // Two hooks on one failing watcher: one reason, about the watcher, which
    // opens the watcher's window; the others open their run, hook or routine.
    let json = """
        {"schemaVersion":1,"updatedAt":"2026-11-22T14:55:10Z","health":"error","busy":false,"routines":[],
         "hooks":[{"name":"home-joke","enabled":true,"watcher":"home-key",
                   "watcherProblem":"home-key is failing: can't listen to the keyboard",
                   "problem":"its watcher home-key is failing: can't listen to the keyboard","health":"error"}],
         "reasons":[{"watcher":"home-key",
                     "text":"home-key is failing: can't listen to the keyboard (home-joke and say-hi listen to it)"},
                    {"routine":"say-hi","hook":true,"text":"say-hi failed","run":"say-hi/r1"},
                    {"routine":"wave","hook":true,"text":"wave can't run","detail":"it isn't trusted"},
                    {"routine":"standup","text":"standup failed","run":"standup/r2"},
                    {"routine":"digest","text":"digest can't be scheduled"},
                    {"text":"The scheduler can't work"}]}
        """
    let status = try RunStatus.decode(Data(json.utf8))
    #expect(
        status.reasons?.map(\.target) == [
            .watcher("home-key"), .hook("say-hi", run: "say-hi/r1"), .hook("wave", run: nil), .run("standup/r2"),
            .routine("digest"), nil,
        ])
    #expect(status.hook("home-joke")?.watcher == "home-key")
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
    #expect(hooks[0].on.text == "When a meeting.started event (from cal)")
    #expect(hooks[1].on.text == "When any event (from cal)")
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

/// What a hook acts on says the label its watcher declares ("When End key
/// pressed (from nav-keys)"); a watcher's window lists its events with the
/// hooks acting on each, or the core's hint when it declares none.
@Test func saysWhatHooksActOnWithTheirWatchersLabels() throws {
    let on = try CoreJSON.decoder().decode(
        Components.Schemas.On.self,
        from: Data(#"{"watcher":"nav-keys","type":"key.end","label":"End key pressed"}"#.utf8))
    #expect(on.text == "When End key pressed (from nav-keys)")
    #expect(on.phrase == "End key pressed (from nav-keys)")
    let show = """
        {"name":"nav-keys","path":"/w/nav-keys/watcher.yaml","mode":"stream","executable":"/w/nav-keys/watch",
         "args":[],"secrets":[],"enabled":true,"trusted":true,"hooks":["on-end"],
         "events":[{"type":"key.home","label":"Home key pressed","hooks":[]},
                   {"type":"key.end","label":"End key pressed","hooks":["on-end","on-any"]}],
         "source":"mode: stream\\n"}
        """
    let w = try CoreJSON.decoder().decode(Components.Schemas.WatcherDetail.self, from: Data(show.utf8))
    #expect(w.events?.map(\.label) == ["Home key pressed", "End key pressed"])
    #expect(w.events?.map(\.hooksText) == ["No hook acts on it", "Heard by on-end, on-any"])
    #expect(w.hintText == nil)
    let old = """
        {"name":"cal","path":"/w/cal/watcher.yaml","mode":"poll","executable":"/w/cal/watch","args":[],"secrets":[],
         "enabled":true,"trusted":true,"hooks":[],"hint":"it doesn't declare the events it prints","source":""}
        """
    let hinted = try CoreJSON.decoder().decode(Components.Schemas.WatcherDetail.self, from: Data(old.utf8))
    #expect(hinted.hintText == "It doesn't declare the events it prints.")
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
    // It declares no events: the core's hint, as a sentence; no events.
    #expect(w.hintText == nil && w.events == nil)
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

// MARK: Turning on

/// A stand-in CLI answering each command (by its arguments) from `replies`
/// ("!message": the core's error), logging what it's given; others get {}.
private func scriptedCLI(_ replies: [String: String]) throws -> (CLI, URL) {
    let dir = FileManager.default.temporaryDirectory.appending(path: "automator-ui-test-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let log = dir.appending(path: "log")
    var cases = ""
    for (index, (args, reply)) in replies.enumerated() {
        let file = dir.appending(path: "reply\(index)")
        let failing = reply.hasPrefix("!")
        let body = failing ? #"{"error":"\#(reply.dropFirst())"}"# : reply
        try body.write(to: file, atomically: true, encoding: .utf8)
        cases += "  '\(args)') cat '\(file.path)'; exit \(failing ? 1 : 0) ;;\n"
    }
    let script = dir.appending(path: "procauto")
    try """
    #!/bin/sh
    echo "$*" >> '\(log.path)'
    case "$*" in
    \(cases)  *) echo '{}' ;;
    esac
    """.write(to: script, atomically: true, encoding: .utf8)
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: script.path)
    return (CLI(executable: script), log)
}

private let hookReview = """
    {"routine":"open-meet","kind":"program","surface":{"servers":[],"tools":[]},"changes":[],"trusted":false,"hook":true,
     "program":{"executable":"/h/act","checksum":"be2b","args":[],"writes":false,"secrets":[]},
     "trigger":{"watcher":"cal","type":"meeting.started"},
     "watcher":{"name":"cal","trusted":false,"untrusted":"not trusted yet","mode":"poll","description":"My calendar",
       "program":{"executable":"/w/watch","checksum":"4ef3","args":[],"writes":false,
         "secrets":[{"name":"cal-token","env":"CAL_TOKEN","stored":false}]}},
     "signIns":["cal"]}
    """

@MainActor
@Test func turningAHookOnIsOneReviewAndOneConfirm() async throws {
    let on = hookReview.replacingOccurrences(
        of: #""trusted":false,"hook""#, with: #""trusted":true,"resumed":true,"hook""#)
    let (cli, log) = try scriptedCLI([
        "hooks trust open-meet --check --json": hookReview,
        "hooks trust open-meet --yes --resume --json": on,
    ])
    let turnOn = TurnOn(name: "open-meet", kind: .hook, cli: cli)
    var reloaded = 0
    turnOn.turnedOn = { reloaded += 1 }
    turnOn.showing = true
    await turnOn.load()
    let review = try #require(turnOn.review)
    // The review covers its watcher, and the sign-in still needed.
    #expect(review.needsTrust && review.confirmTitle == "Trust and Turn On")
    #expect(review.watcherToTrust?.program?.executable == "/w/watch")
    #expect(review.missingWatcher == nil)
    #expect(review.signIns == ["cal"])
    await turnOn.confirm()
    #expect(!turnOn.showing && turnOn.error == nil && reloaded == 1)
    #expect(ran(log) == ["hooks trust open-meet --check --json", "hooks trust open-meet --yes --resume --json"])
}

@MainActor
@Test func turningARoutineOnSaysWhyItCouldnt() async throws {
    let review = #"{"routine":"digest","surface":{"servers":[],"tools":[]},"changes":[],"trusted":true}"#
    let (cli, log) = try scriptedCLI([
        "routines trust digest --check --json": review,
        "routines trust digest --yes --resume --json": "!the config file is locked",
        "routines trust gone --check --json": "!no routine named gone",
    ])
    let turnOn = TurnOn(name: "digest", kind: .routine, cli: cli)
    turnOn.showing = true
    await turnOn.load()
    // Trusted already (paused): nothing to trust, only to turn on.
    #expect(turnOn.review?.confirmTitle == "Turn On")
    await turnOn.confirm()
    // The confirm failed: why stays in the review, which stays open.
    #expect(turnOn.showing && turnOn.error == "the config file is locked" && !turnOn.working)
    #expect(ran(log).last == "routines trust digest --yes --resume --json")
    // A review that can't be read says why.
    let gone = TurnOn(name: "gone", kind: .routine, cli: cli)
    await gone.load()
    #expect(gone.review == nil && gone.loadError == "no routine named gone")
}

@MainActor
@Test func signingInFromTheReviewReadsItAgain() async throws {
    let (cli, log) = try scriptedCLI(["hooks trust open-meet --check --json": hookReview])
    let turnOn = TurnOn(name: "open-meet", kind: .hook, cli: cli)
    await turnOn.signIn("cal")
    #expect(turnOn.signingIn == nil && turnOn.signInError == nil && turnOn.review != nil)
    #expect(ran(log) == ["auth login cal --browser", "hooks trust open-meet --check --json"])
}

// MARK: Logs and bug reports

@Test func logsAndReportsBuildTheirCommands() {
    #expect(CLI.logsArguments("standup", kind: .routine) == ["routines", "logs", "standup"])
    #expect(CLI.logsArguments("open-meet", kind: .hook) == ["hooks", "logs", "open-meet"])
    // Text is given as --flag=value: one starting with "-" isn't a flag.
    #expect(
        CLI.reportArguments(
            "standup", kind: .routine, title: "Posts twice", description: "-- twice at 9:20", includeLogs: true,
            dryRun: false)
            == [
                "routines", "report", "standup", "--title=Posts twice", "--description=-- twice at 9:20",
                "--include-logs",
            ])
    #expect(
        CLI.reportArguments("open-meet", kind: .hook, title: "", description: "", includeLogs: false, dryRun: true)
            == ["hooks", "report", "open-meet", "--title=", "--description=", "--dry-run"])
}

private let logsReply = """
    {"path":"/r/standup-logs-20261127-0920.zip","name":"standup","runs":["standup/2026-11-27T092000"],
     "files":[{"name":"standup.prompt","bytes":812},{"name":"runs/2026-11-27T092000.json","bytes":4096}]}
    """

private func reportReply(opened: Bool, logs: Bool) -> String {
    """
    {"repo":"acme/automator","url":"https://github.com/acme/automator/issues/new?title=x","title":"Posts twice",
     "body":"It posted twice.","opened":\(opened)\(logs ? #","logs":"# + logsReply : "")}
    """
}

@MainActor
@Test func exportingLogsShowsTheZipOrWhyNot() async throws {
    let (cli, log) = try scriptedCLI([
        "routines logs standup --json": logsReply,
        "hooks logs gone --json": "!no hook named gone",
    ])
    let report = BugReport(name: "standup", kind: .routine, cli: cli)
    var revealed: [URL] = []
    report.reveal = { revealed.append($0) }
    await report.exportLogs()
    #expect(revealed == [URL(filePath: "/r/standup-logs-20261127-0920.zip")])
    #expect(report.exportError == nil && !report.exporting)
    let gone = BugReport(name: "gone", kind: .hook, cli: cli)
    gone.reveal = { revealed.append($0) }
    await gone.exportLogs()
    #expect(gone.exportError == "no hook named gone" && revealed.count == 1)
    #expect(ran(log) == ["routines logs standup --json", "hooks logs gone --json"])
    let logs = try CoreJSON.decoder().decode(LogsOutput.self, from: Data(logsReply.utf8))
    #expect(logs.runs == ["standup/2026-11-27T092000"] && logs.files.map(\.bytes) == [812, 4096] && logs.hook == nil)
}

@MainActor
@Test func reportingABugSaysWhereItGoesThenOpensIt() async throws {
    let (cli, log) = try scriptedCLI([
        "routines report standup --title= --description= --dry-run --json": reportReply(opened: false, logs: false),
        "routines report standup --title=Posts twice --description=It posted twice. --json":
            reportReply(opened: true, logs: false),
        "routines report standup --title=Posts twice --description=It posted twice. --include-logs --json":
            reportReply(opened: true, logs: true),
    ])
    let report = BugReport(name: "standup", kind: .routine, cli: cli)
    var revealed: [URL] = []
    report.reveal = { revealed.append($0) }
    report.showing = true
    await report.load()
    #expect(report.repo == "acme/automator" && report.loadError == nil)
    #expect(report.whereText?.contains("on acme/automator") == true)
    // Without logs: opened, it closes, emptied.
    report.title = "Posts twice"
    report.description = "It posted twice."
    await report.open()
    #expect(!report.showing && report.error == nil && report.title.isEmpty && report.logsToDrag == nil)
    // With logs: it stays, to say which zip to drag onto the issue (the core
    // showed it in Finder: the app doesn't again).
    report.showing = true
    report.title = "Posts twice"
    report.description = "It posted twice."
    report.includeLogs = true
    await report.open()
    #expect(report.showing && report.logsToDrag == "standup-logs-20261127-0920.zip" && report.unopened == nil)
    #expect(report.dragText?.hasPrefix("Drag standup-logs-20261127-0920.zip from the Finder window") == true)
    #expect(revealed.isEmpty)
    report.finish()
    #expect(!report.showing && !report.includeLogs && report.logsToDrag == nil)
    #expect(
        ran(log) == [
            "routines report standup --title= --description= --dry-run --json",
            "routines report standup --title=Posts twice --description=It posted twice. --json",
            "routines report standup --title=Posts twice --description=It posted twice. --include-logs --json",
        ])
}

@MainActor
@Test func reportingABugSaysWhyItCouldnt() async throws {
    let refusal =
        "Process Automator takes no bug reports (its build names nowhere to send them): export the logs with `procauto hooks logs open-meet` and send them to whoever maintains it"
    let (cli, _) = try scriptedCLI([
        "hooks report open-meet --title= --description= --dry-run --json": "!\(refusal)",
        "hooks report open-meet --title=x --description= --json": "!no hook named open-meet",
        "hooks report flaky --title=x --description= --json": reportReply(opened: false, logs: false),
    ])
    let report = BugReport(name: "open-meet", kind: .hook, cli: cli)
    report.showing = true
    await report.load()
    // The app takes no reports: the core's words, as they are.
    #expect(report.repo == nil && report.whereText == nil && report.loadError == refusal)
    report.title = "x"
    await report.open()
    #expect(report.showing && report.error == "no hook named open-meet" && !report.working)
    // The browser didn't open: the issue's address stays, to open from the sheet.
    let flaky = BugReport(name: "flaky", kind: .hook, cli: cli)
    flaky.showing = true
    flaky.title = "x"
    await flaky.open()
    #expect(flaky.showing && flaky.unopened?.host() == "github.com" && flaky.logsToDrag == nil)
}

@Test func aHookWithNoWatcherSaysSo() throws {
    let json = """
        {"routine":"lonely","surface":{"servers":[],"tools":[]},"changes":[],"trusted":false,"hook":true,
         "watcher":{"name":"nowhere","trusted":false,"untrusted":"no watcher named nowhere"}}
        """
    let review = try CoreJSON.decoder().decode(Components.Schemas.TrustOutput.self, from: Data(json.utf8))
    #expect(review.watcherToTrust == nil && review.missingWatcher?.name == "nowhere")
    #expect(review.confirmTitle == "Trust and Turn On")
    // Lists say what needs turning on.
    let rows = try CoreJSON.decoder().decode(
        [Components.Schemas.HookInfo].self,
        from: Data(
            #"[{"name":"lonely","path":"/h","on":{"watcher":"nowhere"},"enabled":false,"staleness":"10m","timeout":"20m","rate":60,"trusted":false,"turnOn":true}]"#
                .utf8))
    #expect(rows.first?.turnOn == true)
}

// MARK: Permissions

private func permission(_ json: String) throws -> PermissionNeed {
    try CoreJSON.decoder().decode(PermissionNeed.self, from: Data(json.utf8))
}

private let inputMonitoring = """
    {"permission":"input-monitoring","label":"Input Monitoring","state":"missing","by":[{"kind":"watcher","name":"home-key"}],
     "where":"in System Settings > Privacy & Security > Input Monitoring",
     "settingsUrl":"x-apple.systempreferences:com.apple.preference.security?Privacy_ListenEvent"}
    """

@Test func putsPermissionsInWords() throws {
    let missing = try permission(inputMonitoring)
    #expect(missing.blocking && missing.shown)
    #expect(missing.reason == "for its watcher home-key" && missing.stateText == "Not allowed")
    let notify = try permission(
        #"{"permission":"notifications","label":"Notifications","state":"not-checked","by":[{"kind":"plugin","name":"notify"}]}"#
    )
    #expect(notify.blocking && notify.reason == "for the notify plugin" && notify.stateText == "Not checked yet")
    let granted = try permission(
        #"{"permission":"accessibility","label":"Accessibility","state":"granted","by":[{"kind":"program","name":"x"}]}"#
    )
    #expect(!granted.blocking && granted.shown && granted.reason == "for its program")
    // Unknown and unsupported never block, and aren't shown.
    for state in ["unknown", "unsupported"] {
        let quiet = try permission(
            #"{"permission":"full-disk-access","label":"Full Disk Access","state":"\#(state)","by":[]}"#)
        #expect(!quiet.blocking && !quiet.shown)
    }
    #expect(
        [missing, notify, granted].wontRunText == "It won't run until Input Monitoring and Notifications are allowed.")
    #expect([missing].wontRunText == "It won't run until Input Monitoring is allowed.")
    #expect([granted].wontRunText == nil)
}

@MainActor
@Test func grantAsksThenOpensTheSettingsPage() async throws {
    let prompted = inputMonitoring.replacingOccurrences(of: #""by""#, with: #""asked":true,"by""#)
    let refused = inputMonitoring.replacingOccurrences(of: "input-monitoring", with: "camera")
        .replacingOccurrences(of: #""by""#, with: #""asked":false,"why":"asked before","by""#)
    let (cli, log) = try scriptedCLI([
        "permissions request input-monitoring --json": prompted,
        "permissions request camera --json": refused,
        "permissions request notifications --json":
            #"{"permission":"notifications","label":"Notifications","state":"granted","asked":true,"by":[]}"#,
        "permissions request accessibility --json": "!the scheduler isn't running",
    ])
    let granter = PermissionGranter(cli: cli, app: "Acme")
    var opened: [URL] = []
    var asked = 0
    granter.openURL = { opened.append($0) }
    granter.asked = { asked += 1 }
    await granter.grant("input-monitoring")
    // macOS asked, and its prompt may still be up: nothing opens over it (it
    // and this window would go behind), and it says what to do.
    #expect(opened.isEmpty)
    #expect(
        granter.notes["input-monitoring"]
            == "Answer the prompt, or switch on Acme in System Settings > Privacy & Security > Input Monitoring.")
    // macOS didn't ask and it's still not allowed: its page opens.
    await granter.grant("camera")
    #expect(
        opened.map(\.absoluteString) == ["x-apple.systempreferences:com.apple.preference.security?Privacy_ListenEvent"])
    // Allowed at once: nothing to open.
    await granter.grant("notifications")
    #expect(opened.count == 1 && granter.notes["notifications"] == nil)
    // Asking failed: why, by its button.
    await granter.grant("accessibility")
    #expect(granter.errors["accessibility"] == "the scheduler isn't running" && granter.granting == nil)
    #expect(asked == 4)
    #expect(ran(log).allSatisfy { $0.hasPrefix("permissions request ") })
}

/// Once a permission Grant… asked for is allowed (at once, or later when
/// what needs it reads its permissions again), `granted` says so, once: the
/// app brings its window back in front of the prompt's app or System
/// Settings. One it didn't ask for, or still missing, says nothing.
@MainActor
@Test func grantSaysWhenItsAllowed() async throws {
    let prompted = inputMonitoring.replacingOccurrences(of: #""by""#, with: #""asked":true,"by""#)
    let (cli, _) = try scriptedCLI([
        "permissions request input-monitoring --json": prompted,
        "permissions request notifications --json":
            #"{"permission":"notifications","label":"Notifications","state":"granted","asked":true,"by":[]}"#,
    ])
    let granter = PermissionGranter(cli: cli, app: "Acme")
    var granted: [String] = []
    granter.granted = { granted.append($0) }
    let missing = try JSONDecoder().decode(PermissionNeed.self, from: Data(inputMonitoring.utf8))
    let allowed = try JSONDecoder().decode(
        PermissionNeed.self, from: Data(inputMonitoring.replacingOccurrences(of: "missing", with: "granted").utf8))
    let camera = try JSONDecoder().decode(
        PermissionNeed.self,
        from: Data(
            inputMonitoring.replacingOccurrences(of: "input-monitoring", with: "camera")
                .replacingOccurrences(of: "missing", with: "granted").utf8))

    granter.saw([allowed])  // not asked for: nothing
    await granter.grant("input-monitoring")
    #expect(granted.isEmpty && granter.notes["input-monitoring"] != nil)
    granter.saw([missing, camera])
    #expect(granted.isEmpty)
    granter.saw([allowed])
    #expect(granted == ["input-monitoring"] && granter.notes["input-monitoring"] == nil)
    granter.saw([allowed])
    #expect(granted == ["input-monitoring"])
    // Allowed before asking returned (answered in the prompt at once).
    await granter.grant("notifications")
    #expect(granted == ["input-monitoring", "notifications"])
}

/// A stand-in CLI whose `trust --check` says the permission is missing the
/// first time and granted after (the scheduler recorded it meanwhile).
private func permissionFlippingCLI() throws -> CLI {
    let dir = FileManager.default.temporaryDirectory.appending(path: "automator-ui-test-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let review = """
        {"routine":"tell","surface":{"servers":["notify"],"tools":["notify_me"]},"changes":[],"trusted":false,"hook":true,
         "permissions":[\(inputMonitoring)]}
        """
    try review.write(to: dir.appending(path: "missing"), atomically: true, encoding: .utf8)
    try review.replacingOccurrences(of: #""state":"missing""#, with: #""state":"granted""#)
        .write(to: dir.appending(path: "granted"), atomically: true, encoding: .utf8)
    let script = dir.appending(path: "procauto")
    try """
    #!/bin/sh
    if [ -e '\(dir.path)/asked' ]; then cat '\(dir.path)/granted'; else touch '\(dir.path)/asked'; cat '\(dir.path)/missing'; fi
    """.write(to: script, atomically: true, encoding: .utf8)
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: script.path)
    return CLI(executable: script)
}

@MainActor
@Test func aPermissionInTheReviewTurnsAllowedByItself() async throws {
    let turnOn = TurnOn(name: "tell", kind: .hook, cli: try permissionFlippingCLI(), app: "Acme")
    turnOn.pollEvery = nil
    turnOn.showing = true
    await turnOn.load()
    #expect(turnOn.waitingForPermission)
    #expect(turnOn.review?.permissions?.blocking.map(\.label) == ["Input Monitoring"])
    // Run status changed: the open review is read again, and the row is allowed.
    await turnOn.statusChanged()
    #expect(!turnOn.waitingForPermission)
    #expect(turnOn.review?.permissions?.first?.state == .granted)
    // A closed review isn't read on a status change.
    turnOn.showing = false
    await turnOn.statusChanged()
}

// MARK: Acknowledgements

private let waitingNote = """
    [{"id":"n7","at":"2026-11-23T09:00:00Z","kind":"run","title":"Scout found a match","body":"3 new listings",
      "routine":"marco-scout","run":"marco-scout/2026-11-23T090000","acknowledge":true}]
    """

@Test func decodesWhatWaitsForAcknowledging() throws {
    // The reason that's the menu's status line opens the Acknowledgements window.
    let status = try RunStatus.decode(
        Data(
            """
            {"schemaVersion":1,"updatedAt":"2026-11-23T09:00:00Z","health":"error","busy":false,"routines":[],
             "pendingAcknowledgements":2,"waiting":[{"id":"n7","title":"a"},{"id":"n8","title":"b"}],
             "reasons":[{"text":"(2) Acknowledgements pending","detail":"a\\nb","acknowledge":true},
                        {"routine":"digest","text":"digest can't be scheduled"}]}
            """.utf8))
    #expect(status.reasons?.map(\.target) == [.acknowledgements, .routine("digest")])
    #expect(status.waiting?.map(\.id) == ["n7", "n8"])
    let notes = try CoreJSON.decoder().decode([CoreNotification].self, from: Data(waitingNote.utf8))
    #expect(notes[0].needsAcknowledging)
    let acked = try CoreJSON.decoder().decode(
        [CoreNotification].self,
        from: Data(
            waitingNote.replacingOccurrences(
                of: #""acknowledge":true"#, with: #""acknowledge":true,"acknowledgedAt":"2026-11-23T09:05:00Z""#
            )
            .utf8))
    #expect(!acked[0].needsAcknowledging)
}

@MainActor
@Test func acknowledgingRunsTheCoresCommands() async throws {
    let (cli, log) = try scriptedCLI([
        "notifications list --waiting --json": waitingNote,
        "notifications list --acknowledged --json": "[]",
        "notifications ack n7 --json": #"{"acknowledged":[]}"#,
        "notifications ack --all --json": "!the notifications file is locked",
    ])
    let model = Acknowledgements(cli: cli)
    let status = { (ids: [String]) in
        try RunStatus.decode(
            Data(
                """
                {"schemaVersion":1,"updatedAt":"2026-11-23T09:00:00Z","health":"error","busy":false,"routines":[],
                 "pendingAcknowledgements":\(ids.count),"waiting":[\(ids.map { #"{"id":"\#($0)","title":"t"}"# }.joined(separator: ","))]}
                """.utf8))
    }
    await model.update(try status(["n7"]))
    #expect(model.waiting.map(\.title) == ["Scout found a match"])
    // The same notifications waiting (a heartbeat): not read again.
    await model.update(try status(["n7"]))
    await model.acknowledge("n7")
    #expect(model.error == nil && model.acknowledging.isEmpty)
    await model.acknowledgeAll()
    #expect(model.error == "the notifications file is locked")
    #expect(
        ran(log) == [
            "notifications list --waiting --json", "notifications list --acknowledged --json",
            "notifications ack n7 --json", "notifications list --waiting --json",
            "notifications list --acknowledged --json", "notifications ack --all --json",
            "notifications list --waiting --json", "notifications list --acknowledged --json",
        ])
}

/// A core whose notifications change as they're acknowledged: n7 waits
/// until `notifications ack n7`, then it's the last acknowledged; seven
/// older ones were acknowledged before (p1 first).
private func acknowledgingCLI() throws -> CLI {
    let dir = FileManager.default.temporaryDirectory.appending(path: "automator-ui-test-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let older = (1...7).map {
        #"{"id":"p\#($0)","at":"2026-11-20T09:00:00Z","kind":"run","title":"Past \#($0)","acknowledge":true,"#
            + #""acknowledgedAt":"2026-11-20T1\#($0):00:00Z"}"#
    }
    let n7Acked =
        #"{"id":"n7","at":"2026-11-23T09:00:00Z","kind":"run","title":"Scout found a match","#
        + #""routine":"marco-scout","run":"marco-scout/2026-11-23T090000","acknowledge":true,"#
        + #""acknowledgedAt":"2026-11-23T09:05:00Z"}"#
    try waitingNote.write(to: dir.appending(path: "waiting"), atomically: true, encoding: .utf8)
    try "[\(older.joined(separator: ","))]".write(to: dir.appending(path: "before"), atomically: true, encoding: .utf8)
    try "[\((older + [n7Acked]).joined(separator: ","))]".write(
        to: dir.appending(path: "after"), atomically: true, encoding: .utf8)
    let script = dir.appending(path: "procauto")
    try """
    #!/bin/sh
    cd '\(dir.path)'
    case "$*" in
      'notifications list --waiting --json') if [ -f acked ]; then echo '[]'; else cat waiting; fi ;;
      'notifications list --acknowledged --json') if [ -f acked ]; then cat after; else cat before; fi ;;
      'notifications ack n7 --json') touch acked; echo '{"acknowledged":[]}' ;;
      *) echo '{}' ;;
    esac
    """.write(to: script, atomically: true, encoding: .utf8)
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: script.path)
    return CLI(executable: script)
}

@MainActor
@Test func pastAcknowledgementsLoadTheMostRecentFirst() async throws {
    let model = Acknowledgements(cli: try acknowledgingCLI())
    await model.load()
    #expect(model.waiting.map(\.id) == ["n7"])
    #expect(model.pastError == nil)
    // The last acknowledged first; the most recent few showing, Show More
    // showing the rest.
    #expect(model.past.map(\.id) == ["p7", "p6", "p5", "p4", "p3", "p2", "p1"])
    #expect(model.pastShowing.map(\.id) == ["p7", "p6", "p5", "p4", "p3"])
    #expect(model.past.allSatisfy { !$0.needsAcknowledging && $0.acknowledgedAt != nil })
    model.showMorePast()
    #expect(model.pastShowing.count == 7)
}

@MainActor
@Test func acknowledgingOneMovesItToThePast() async throws {
    let model = Acknowledgements(cli: try acknowledgingCLI())
    await model.load()
    #expect(!model.past.contains { $0.id == "n7" })
    await model.acknowledge("n7")
    #expect(model.waiting.isEmpty)
    #expect(model.past.first?.id == "n7")
    #expect(model.past.first?.title == "Scout found a match")
    #expect(model.past.count == 8)
}

@MainActor
@Test func pastAcknowledgementsSayWhyTheyCouldntBeRead() async throws {
    let (cli, _) = try scriptedCLI([
        "notifications list --waiting --json": "[]",
        "notifications list --acknowledged --json": "!the notifications file is locked",
    ])
    let model = Acknowledgements(cli: cli)
    await model.load()
    #expect(model.loadError == nil && model.waiting.isEmpty)
    #expect(model.pastError == "the notifications file is locked")
}

@Test func setupsReasonOpensSetup() throws {
    // What only needs turning on is setup's: its one reason opens setup.
    let status = try RunStatus.decode(
        Data(
            """
            {"schemaVersion":1,"updatedAt":"2026-11-23T09:00:00Z","health":"error","busy":false,"routines":[],
             "hooks":[{"name":"pr-reviewed","enabled":true,"problem":"it isn't trusted","turnOn":true,"health":"error",
                       "summary":"Needs turning on"}],
             "setupLeft":["Turn on pr-reviewed"],
             "reasons":[{"text":"Finish Setup (1 to do)…","detail":"Still to do: Turn on pr-reviewed","setup":true}]}
            """.utf8))
    #expect(status.reasons?.map(\.target) == [.setup])
    #expect(status.hooks?.first?.turnOn == true)
}

// MARK: Pending actions

@MainActor
@Test func aPendingActionWaitsForItsState() async throws {
    let pending = PendingAction()
    var running = false
    // Run: the command returns, and it stays "Starting…" until run status
    // says it runs.
    let isRunning: @MainActor () -> Bool = { running }
    let failure = await pending.run("run", label: "Starting…", until: isRunning) { nil }
    #expect(failure == nil && pending.isPending("run") && pending.busy)
    #expect(pending.pending?.label == "Starting…")
    pending.settle()
    #expect(pending.isPending("run"))
    running = true
    pending.settle()
    #expect(!pending.busy)
    // A failure ends it at once, and says why.
    let never: @MainActor () -> Bool = { false }
    let refused = await pending.run("stop", label: "Stopping…", until: never) { "standup isn't running" }
    #expect(refused == "standup isn't running" && !pending.busy)
    // With nothing to wait for, it ends when the command returns.
    await pending.run("dry-run", label: "Starting dry run…") { nil }
    #expect(!pending.busy)
    // Its state never shows: after the timeout, it ends as things are.
    pending.timeout = .milliseconds(50)
    await pending.run("pause", label: "Pausing…", until: never) { nil }
    #expect(pending.isPending("pause"))
    try await Task.sleep(for: .milliseconds(300))
    #expect(!pending.busy)
}

@Test func readsTheToolCallRecord() throws {
    let run = """
        {"text":"","writes":[],"dryRun":false,"backend":"claude-code","toolCalls":3,"inputTokens":0,"outputTokens":0,
         "cachedTokens":0,"promptSource":"p","outcome":"nothing_to_do","case":"My update is already in the thread",
         "evidence":"my reply at 09:12","calls":[
          {"tool":"read","args":{},"write":false,"at":"2026-11-25T09:00:00Z","result":"latest: April","truncated":true,"resultSize":9000},
          {"tool":"post","args":{"text":"hi"},"write":true,"error":"not_in_channel"},
          {"tool":"post","args":{},"write":true,"intercepted":true,"result":"DRY RUN"}]}
        """
    let r = try CoreJSON.decoder().decode(Components.Schemas.Result.self, from: Data(run.utf8))
    let calls = try #require(r.calls)
    #expect(r._case == "My update is already in the thread")
    #expect(r.evidence == "my reply at 09:12")
    #expect(calls[0].output == "latest: April")
    #expect(calls[0].truncatedText == "Clipped: 13 bytes of 9 KiB kept.")
    #expect(calls[1].failed && calls[1].output == "not_in_channel" && calls[1].truncatedText == nil)
    #expect(calls[2].intercepted == true)
}

// MARK: Apps plugins need

@Test func openingAPluginsAppRunsItsCommand() {
    #expect(CLI.openAppArguments("imcp") == ["plugins", "open-app", "imcp"])
    let json =
        #"{"step":"app","subject":"imcp","title":"Keep iMCP running","done":false,"status":"Not running; used by standup"}"#
    let step = try? JSONDecoder().decode(Components.Schemas.SetupStep.self, from: Data(json.utf8))
    #expect(step?.step == .app && step?.subject == "imcp")
}
