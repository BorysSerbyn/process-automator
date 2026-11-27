import Foundation

/// The Go core's CLI, automator-ui's only way to ask the core anything.
/// Every call blocks until the command exits: run them with `background`.
public struct CLI: Sendable {
    public let executable: URL
    /// How long a command may take before it's stopped and the call fails:
    /// one that hangs (a keyring prompt nobody sees, a stuck server) must
    /// not hold up what waits for it. Sign-in has no limit (it waits for me).
    public var timeout: TimeInterval = 120

    public init(executable: URL) { self.executable = executable }

    /// The core's binary (`AppInfo.command`) bundled next to automator-ui in the .app.
    public static var bundled: CLI? {
        guard let dir = Bundle.main.executableURL?.deletingLastPathComponent() else { return nil }
        let url = dir.appending(path: AppInfo.command)
        return FileManager.default.isExecutableFile(atPath: url.path) ? CLI(executable: url) : nil
    }

    /// Runs CLI calls off the main thread and off Swift's shared task
    /// threads (they block while the command runs).
    public func background<T: Sendable>(_ work: @escaping @Sendable (CLI) -> T) async -> T {
        await withCheckedContinuation { done in
            DispatchQueue.global(qos: .userInitiated).async { done.resume(returning: work(self)) }
        }
    }

    /// Why a command failed, in the core's words.
    public struct Failure: Error, Sendable {
        public let message: String

        public init(message: String) { self.message = message }
    }

    /// What a command printed, and whether it succeeded.
    public struct Output: Sendable {
        public let stdout: Data
        public let stderr: String
        public let ok: Bool

        /// Why it failed: the core's error object (commands given --json
        /// print one), else what it wrote to stderr.
        public var failure: Failure {
            if let error = try? JSONDecoder().decode(Components.Schemas.ErrorOutput.self, from: stdout) {
                return Failure(message: error.error)
            }
            let text = stderr.trimmingCharacters(in: .whitespacesAndNewlines)
            let message = text.hasPrefix("Error: ") ? String(text.dropFirst(7)) : text
            return Failure(message: message.isEmpty ? "\(AppInfo.command) failed" : message)
        }
    }

    /// Runs a command and returns what it printed. `timeout` (default: the
    /// CLI's) stops it, failing the call; `.infinity` waits as long as it runs.
    public func exec(_ arguments: [String], timeout: TimeInterval? = nil) -> Output {
        let process = Process()
        process.executableURL = executable
        process.arguments = arguments
        let out = Pipe()
        let err = Pipe()
        process.standardOutput = out
        process.standardError = err
        guard (try? process.run()) != nil else {
            return Output(stdout: Data(), stderr: "couldn't start \(AppInfo.command)", ok: false)
        }
        // Read stderr alongside stdout, so neither pipe fills up and blocks.
        let errData = LockedData()
        let errRead = DispatchSemaphore(value: 0)
        Thread {
            errData.set(err.fileHandleForReading.readDataToEndOfFile())
            errRead.signal()
        }.start()
        let limit = timeout ?? self.timeout
        let timedOut = LockedFlag()
        var deadline: DispatchWorkItem?
        if limit.isFinite {
            let pid = process.processIdentifier
            let item = DispatchWorkItem {
                timedOut.set()
                kill(pid, SIGTERM)
            }
            DispatchQueue.global().asyncAfter(deadline: .now() + limit, execute: item)
            deadline = item
        }
        let data = out.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        deadline?.cancel()
        errRead.wait()
        if timedOut.get() {
            let what = ([AppInfo.command] + arguments).joined(separator: " ")
            return Output(stdout: Data(), stderr: "\(what) didn't answer in \(Self.duration(limit))", ok: false)
        }
        let stderr = String(decoding: errData.get(), as: UTF8.self)
        return Output(stdout: data, stderr: stderr, ok: process.terminationStatus == 0)
    }

    /// A limit in words: "2 minutes", "30 seconds".
    static func duration(_ seconds: TimeInterval) -> String {
        let s = Int(seconds.rounded())
        return s >= 60 && s % 60 == 0 ? "\(s / 60) minute\(s == 60 ? "" : "s")" : "\(s) second\(s == 1 ? "" : "s")"
    }

    /// Runs a command given --json and decodes what it printed.
    /// `trailing` arguments go after `--` (free text); `timeout` as exec's.
    func call<T: Decodable>(
        _ type: T.Type, _ arguments: [String], trailing: [String] = [], timeout: TimeInterval? = nil
    ) -> Result<T, Failure> {
        let output = exec(arguments + ["--json"] + (trailing.isEmpty ? [] : ["--"] + trailing), timeout: timeout)
        guard output.ok else { return .failure(output.failure) }
        do {
            return .success(try CoreJSON.decoder().decode(T.self, from: output.stdout))
        } catch {
            return .failure(Failure(message: "\(AppInfo.command) answered something unexpected: \(error)"))
        }
    }

    /// Runs a command for what it does; the core's message when it fails.
    func perform(_ arguments: [String], timeout: TimeInterval? = nil) -> Failure? {
        let output = exec(arguments, timeout: timeout)
        return output.ok ? nil : output.failure
    }

    /// App-wide settings (`settings list --scope app`).
    public func appSettings() -> Result<[Components.Schemas.State], Failure> {
        call([Components.Schemas.State].self, ["settings", "list", "--scope", "app"])
    }

    /// Sets a setting from its text.
    public func setSetting(_ key: String, _ text: String) -> Result<Components.Schemas.State, Failure> {
        call(Components.Schemas.State.self, ["settings", "set", key, text])
    }

    /// Puts a setting back to its default.
    public func resetSetting(_ key: String) -> Result<Components.Schemas.State, Failure> {
        call(Components.Schemas.State.self, ["settings", "reset", key])
    }

    /// Where updates stand, from what the core already knows (`update status`):
    /// this version, automatic updates, the latest release the scheduler found.
    public func updateStatus() -> Result<Components.Schemas.UpdateStatus, Failure> {
        call(Components.Schemas.UpdateStatus.self, ["update", "status"])
    }

    /// A release's notes, this version's when `tag` is nil (`update notes`).
    public func releaseNotes(_ tag: String? = nil) -> Result<Components.Schemas.NotesOutput, Failure> {
        call(Components.Schemas.NotesOutput.self, ["update", "notes"] + (tag.map { [$0] } ?? []))
    }

    /// Whether a newer release exists, and whether one is installing (`update --check`).
    public func updateCheck() -> Result<Components.Schemas.UpdateOutput, Failure> {
        call(Components.Schemas.UpdateOutput.self, ["update", "--check"])
    }

    /// Starts installing the newer release in the background; the app restarts
    /// when it's done, and the result comes as a notification.
    public func startUpdate() -> Result<Components.Schemas.UpdateOutput, Failure> {
        call(Components.Schemas.UpdateOutput.self, ["update", "--detach"])
    }

    /// What the update installing is doing, or how the latest one ended
    /// (`update progress`).
    public func updateProgress() -> Result<Components.Schemas.UpdateProgress, Failure> {
        call(Components.Schemas.UpdateProgress.self, ["update", "progress"])
    }

    /// Whether the app starts at login.
    public func loginItems() -> Result<Components.Schemas.LoginItemsOutput, Failure> {
        call(Components.Schemas.LoginItemsOutput.self, ["login-items", "status"])
    }

    /// Turns starting at login on or off.
    public func setStartAtLogin(_ on: Bool) -> Result<Components.Schemas.LoginItemsOutput, Failure> {
        call(Components.Schemas.LoginItemsOutput.self, ["login-items", on ? "enable" : "disable"])
    }

    /// Starts the scheduler if it isn't running (`login-items start`):
    /// opening the app does.
    public func startScheduler() -> Result<Components.Schemas.RunningOutput, Failure> {
        call(Components.Schemas.RunningOutput.self, ["login-items", "start"])
    }

    /// Stops the scheduler, and any other menu bar app running (`login-items
    /// stop`, which leaves the one that ran it to quit): Quit does, then quits.
    public func stopApp() -> Result<Components.Schemas.RunningOutput, Failure> {
        call(Components.Schemas.RunningOutput.self, ["login-items", "stop"])
    }

    /// Opens a readable report of a run (the core writes and opens it).
    public func openRun(_ id: String) -> Failure? {
        perform(["runs", "show", id, "--open"])
    }

    // MARK: Routines

    /// Every routine with its effective settings (`routines list`).
    public func routines() -> Result<[Components.Schemas.RoutineInfo], Failure> {
        call([Components.Schemas.RoutineInfo].self, ["routines", "list", "--kind", "all"])
    }

    /// One routine in full (`routines show`).
    public func routine(_ name: String) -> Result<Components.Schemas.RoutineDetail, Failure> {
        call(Components.Schemas.RoutineDetail.self, ["routines", "show", name])
    }

    /// A routine's run history, newest first (`runs <routine>`).
    public func runs(_ routine: String, limit: Int) -> Result<[Components.Schemas.Summary], Failure> {
        call([Components.Schemas.Summary].self, ["runs", routine, "--limit", String(limit)])
    }

    /// One run in full: its result, writes and final text (`runs show`).
    public func run(_ id: String) -> Result<Components.Schemas.Entry, Failure> {
        call(Components.Schemas.Entry.self, ["runs", "show", id])
    }

    /// A routine's own settings (`settings list --routine`).
    public func routineSettings(_ routine: String) -> Result<[Components.Schemas.State], Failure> {
        call([Components.Schemas.State].self, ["settings", "list", "--routine", routine])
    }

    /// Starts a run in the background (`start`); run status follows it.
    public func start(_ routine: String, dryRun: Bool) -> Result<Components.Schemas.StartOutput, Failure> {
        call(Components.Schemas.StartOutput.self, ["start", routine] + (dryRun ? ["--dry-run"] : []))
    }

    /// Stops a routine's run in progress, or waiting for its turn (`stop`).
    public func stop(_ routine: String) -> Result<Components.Schemas.StopOutput, Failure> {
        call(Components.Schemas.StopOutput.self, ["stop", routine])
    }

    /// Pauses a routine's schedule (`routines pause`), or resumes it (`routines resume`).
    public func setRoutinePaused(_ routine: String, _ paused: Bool) -> Result<Components.Schemas.RoutineChange, Failure>
    {
        call(Components.Schemas.RoutineChange.self, ["routines", paused ? "pause" : "resume", routine])
    }

    /// Deletes a routine: its file, settings, trust and memories (`routines delete`).
    public func deleteRoutine(_ routine: String) -> Result<Components.Schemas.RoutineChange, Failure> {
        call(Components.Schemas.RoutineChange.self, ["routines", "delete", routine])
    }

    /// What a routine can reach and what changed since it was trusted, trusting
    /// nothing (`routines trust --check`).
    public func trustCheck(_ routine: String) -> Result<Components.Schemas.TrustOutput, Failure> {
        call(Components.Schemas.TrustOutput.self, ["routines", "trust", routine, "--check"])
    }

    /// Trusts a routine to run for real, after the user reviewed `trustCheck`.
    public func trust(_ routine: String) -> Result<Components.Schemas.TrustOutput, Failure> {
        call(Components.Schemas.TrustOutput.self, ["routines", "trust", routine, "--yes"])
    }

    /// Turns a routine on in one step, after the user reviewed `trustCheck`:
    /// trusts it and lets it run on its schedule (`routines trust --resume`).
    public func turnOn(_ routine: String) -> Result<Components.Schemas.TrustOutput, Failure> {
        call(Components.Schemas.TrustOutput.self, ["routines", "trust", routine, "--yes", "--resume"])
    }

    // MARK: Setup and usage

    /// Where first-run setup stands: every step, in order, changing nothing (`setup`).
    public func setup() -> Result<Components.Schemas.SetupOutput, Failure> {
        call(Components.Schemas.SetupOutput.self, ["setup"])
    }

    /// The templates that can be installed as routines (`templates list`).
    public func templates() -> Result<[Components.Schemas.TemplateInfo], Failure> {
        call([Components.Schemas.TemplateInfo].self, ["templates", "list"])
    }

    /// Installs a template as a routine, with its parameters' values (`templates install`).
    public func installTemplate(_ id: String, values: [(name: String, value: String)])
        -> Result<Components.Schemas.InstallOutput, Failure>
    {
        let sets = values.flatMap { ["--set", "\($0.name)=\($0.value)"] }
        return call(Components.Schemas.InstallOutput.self, ["templates", "install", id] + sets)
    }

    /// Signs in to a server, or stores the Anthropic API key (`auth login
    /// --browser`: OAuth in the browser, keys on a local page there). It
    /// blocks until that's done.
    public func signIn(_ server: String) -> Failure? {
        perform(["auth", "login", server, "--browser"], timeout: .infinity)  // waits while I sign in
    }

    /// Records the Claude plan's usage from Claude Code's status line: wraps
    /// the user's status line command with the core's, which still runs
    /// theirs (`usage statusline install`).
    public func installStatusLine() -> Result<Components.Schemas.StatusLineOutput, Failure> {
        call(Components.Schemas.StatusLineOutput.self, ["usage", "statusline", "install"])
    }

    /// Opens the desktop app a plugin needs running, unless it's running
    /// (`plugins open-app`).
    public func openApp(plugin: String) -> Result<Components.Schemas.OpenAppOutput, Failure> {
        call(Components.Schemas.OpenAppOutput.self, Self.openAppArguments(plugin))
    }

    /// `plugins open-app <plugin>`.
    public static func openAppArguments(_ plugin: String) -> [String] { ["plugins", "open-app", plugin] }

    /// Registers the core's MCP server with Claude Code, at user scope
    /// (`mcp connect`).
    public func connectClaudeCode() -> Result<Components.Schemas.MCPConnectOutput, Failure> {
        call(Components.Schemas.MCPConnectOutput.self, ["mcp", "connect"])
    }

    /// Spend against each LLM budget (`usage`).
    public func usage() -> Result<Components.Schemas.Report, Failure> {
        call(Components.Schemas.Report.self, ["usage"])
    }

    // MARK: Schedules

    /// What a cron schedule means, when it next runs, and its days and time
    /// when it's one (`schedule describe`).
    public func describeSchedule(_ cron: String) -> Result<Components.Schemas.Info, Failure> {
        call(Components.Schemas.Info.self, ["schedule", "describe"], trailing: [cron])
    }

    /// The schedule for days of the week and a time (`schedule build`).
    public func buildSchedule(days: [Components.Schemas.Picker.daysPayloadPayload], at: String)
        -> Result<Components.Schemas.Info, Failure>
    {
        let names = days.map(\.rawValue).joined(separator: ",")
        return call(Components.Schemas.Info.self, ["schedule", "build", "--days", names, "--at", at])
    }

    // MARK: Memories

    /// A routine's own memories (`memory list --routine`).
    public func memories(_ routine: String) -> Result<[Components.Schemas.Memory], Failure> {
        call([Components.Schemas.Memory].self, ["memory", "list", "--routine", routine])
    }

    /// Adds a memory to a routine's own. The text goes after `--`, so one
    /// starting with "-" isn't read as a flag.
    public func addMemory(_ text: String, routine: String) -> Result<Components.Schemas.Memory, Failure> {
        call(Components.Schemas.Memory.self, ["memory", "add", "--routine", routine], trailing: [text])
    }

    /// Forgets a memory.
    public func removeMemory(_ id: String) -> Failure? {
        perform(["memory", "rm", id])
    }

    /// The app's own notifications not delivered yet, oldest first
    /// (`notifications list --pending`).
    public func pendingNotifications() -> Result<[CoreNotification], Failure> {
        call([CoreNotification].self, ["notifications", "list", "--pending"])
    }

    /// Records that notifications were shown.
    public func markDelivered(_ ids: [String]) {
        _ = perform(["notifications", "delivered"] + ids)
    }

    /// What clicking a notification shows (`notifications open`): its run in
    /// its routine's window, or the app.
    public func openNotification(_ id: String) -> Result<Components.Schemas.ClickOutput, Failure> {
        call(Components.Schemas.ClickOutput.self, ["notifications", "open", id])
    }
}

/// Data written by one thread and read by another.
final class LockedFlag: @unchecked Sendable {
    private let lock = NSLock()
    private var value = false

    func set() {
        lock.lock()
        value = true
        lock.unlock()
    }

    func get() -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return value
    }
}

final class LockedData: @unchecked Sendable {
    private let lock = NSLock()
    private var data = Data()

    func set(_ value: Data) {
        lock.lock()
        data = value
        lock.unlock()
    }

    func get() -> Data {
        lock.lock()
        defer { lock.unlock() }
        return data
    }
}
