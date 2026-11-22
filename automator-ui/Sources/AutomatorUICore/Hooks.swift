import Foundation
import OpenAPIRuntime

/// Hooks and watchers as their windows put them: words for what the core
/// says, nothing worked out.

extension Components.Schemas.On {
    /// What a hook acts on, in words: "calendar's meeting.started events",
    /// or "every event from calendar" (no type).
    public var text: String {
        guard let type = _type, !type.isEmpty else { return "every event from \(watcher)" }
        return "\(watcher)'s \(type) events"
    }
}

extension Components.Schemas.HookInfo {
    /// A program hook (an executable), not an LLM hook: it has no servers,
    /// tools, model or memories.
    public var isProgram: Bool { kind == .program }
}

extension Components.Schemas.HookDetail {
    public var isProgram: Bool { kind == .program }
}

/// What a hook's settings mean, in words.
public enum HookWords {
    /// What a hook's kind is called in its lists: a prompt or a program.
    public static func kind(program: Bool) -> String { program ? "Program" : "Prompt" }

    /// "Events older than 2m are dropped."
    public static func staleness(_ staleness: String) -> String {
        "Events older than \(staleness) are dropped."
    }

    /// "At most 20 runs an hour; far past it, it's paused."
    public static func rate(_ rate: Int) -> String {
        "At most \(rate == 1 ? "1 run" : "\(rate) runs") an hour; far past it, it's paused."
    }

    /// Whether an LLM hook takes the events that waited for it together.
    public static func batch(_ batch: Bool) -> String {
        batch ? "One run gets every event that waited for it." : "One run per event."
    }

    /// What a dry run of it needs: a program hook needs trust even for one;
    /// an LLM hook's has its writes intercepted and needs none.
    public static func dryRun(program: Bool) -> String {
        program
            ? "A program hook needs trusting even for a dry run."
            : "A dry run intercepts its writes, and needs no trust."
    }

    /// The sample event Fire starts from: one line as its watcher would
    /// print it, of the type it acts on.
    public static func sampleEvent(_ on: Components.Schemas.On) -> String {
        let type = (on._type?.isEmpty == false ? on._type : nil) ?? "example"
        return """
            {
              "id": "sample-1",
              "type": "\(type)",
              "data": {}
            }
            """
    }

    /// What happens to the hooks listening when a watcher is deleted.
    public static let deleteWatcher =
        "Its copy, settings, trust, secrets and state are deleted. The hooks listening to it stay, and hear nothing."

    /// What deleting a hook removes and keeps.
    public static let deleteHook = "Its copy, settings, trust and secrets are deleted. Its run history is kept."
}

extension Components.Schemas.WatcherInfo {
    /// How it runs, in words: "Polled every 30s", "Streams events".
    public var modeText: String { WatcherWords.mode(mode.rawValue, interval: interval) }
}

extension Components.Schemas.WatcherDetail {
    public var modeText: String { WatcherWords.mode(mode.rawValue, interval: interval) }

    /// What trusting it approves, as a program's rows show it.
    public var program: Components.Schemas.ProgramInfo {
        .init(args: args, checksum: checksum, executable: executable, secrets: secrets, writes: false)
    }

    /// Whether a secret it declares isn't stored: it needs a sign-in
    /// (`auth login <watcher>`).
    public var needsSignIn: Bool { secrets.contains { !$0.stored } }
}

public enum WatcherWords {
    static func mode(_ mode: String, interval: String?) -> String {
        guard mode == "poll" else { return "Streams events" }
        return interval.map { "Polled every \($0)" } ?? "Polled"
    }
}

extension Components.Schemas.Event {
    /// Its data as plain text: indented JSON, keys in order. It's outside
    /// data, so it's only ever shown as text.
    public var dataText: String {
        guard let data,
            let encoded = try? JSONEncoder().encode(data),
            let object = try? JSONSerialization.jsonObject(with: encoded),
            let pretty = try? JSONSerialization.data(
                withJSONObject: object, options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes])
        else { return "" }
        let text = String(decoding: pretty, as: UTF8.self)
        return text == "{\n\n}" || text == "{}" ? "" : text
    }
}

extension RunStatus {
    /// A hook's entry, by name (never among routines).
    public func hook(_ name: String) -> Components.Schemas.RunStatusRoutine? {
        hooks?.first { $0.name == name }
    }
}

extension CLI {
    // MARK: Hooks

    /// Every hook with its effective settings (`hooks list`).
    public func hooks() -> Result<[Components.Schemas.HookInfo], Failure> {
        call([Components.Schemas.HookInfo].self, ["hooks", "list"])
    }

    /// One hook in full, with its watcher (`hooks show`).
    public func hook(_ name: String) -> Result<Components.Schemas.HookDetail, Failure> {
        call(Components.Schemas.HookDetail.self, ["hooks", "show", name])
    }

    /// What a hook acts on and can reach, and what changed since it was
    /// trusted, trusting nothing (`hooks trust --check`).
    public func hookTrustCheck(_ name: String) -> Result<Components.Schemas.TrustOutput, Failure> {
        call(Components.Schemas.TrustOutput.self, ["hooks", "trust", name, "--check"])
    }

    /// Trusts a hook after the user reviewed `hookTrustCheck`; `turnOn` also
    /// turns it on (`--resume`).
    public func trustHook(_ name: String, turnOn: Bool) -> Result<Components.Schemas.TrustOutput, Failure> {
        call(Components.Schemas.TrustOutput.self, ["hooks", "trust", name, "--yes"] + (turnOn ? ["--resume"] : []))
    }

    /// Turns a hook off (`hooks pause`), or on again (`hooks resume`).
    public func setHookPaused(_ name: String, _ paused: Bool) -> Result<Components.Schemas.HookChange, Failure> {
        call(Components.Schemas.HookChange.self, ["hooks", paused ? "pause" : "resume", name])
    }

    /// Deletes a hook: its copy, settings, trust and secrets (`hooks delete`).
    public func deleteHook(_ name: String) -> Result<Components.Schemas.HookChange, Failure> {
        call(Components.Schemas.HookChange.self, ["hooks", "delete", name])
    }

    /// Runs a hook for a sample event (`hooks fire --event-json`), as a dry
    /// run or for real. It waits for the run, which its own timeout ends.
    public func fireHook(_ name: String, event: String, dryRun: Bool) -> Result<Components.Schemas.RunOutput, Failure> {
        let output = exec(
            ["hooks", "fire", name, "--event-json", event] + (dryRun ? ["--dry-run"] : []) + ["--json"],
            timeout: .infinity)
        guard output.ok else { return .failure(output.failure) }
        do {
            return .success(try CoreJSON.decoder().decode(Components.Schemas.RunOutput.self, from: output.stdout))
        } catch {
            return .failure(Failure(message: "\(AppInfo.command) answered something unexpected: \(error)"))
        }
    }

    // MARK: Watchers

    /// Every watcher, with the hooks listening and how it last went (`watchers list`).
    public func watchers() -> Result<[Components.Schemas.WatcherInfo], Failure> {
        call([Components.Schemas.WatcherInfo].self, ["watchers", "list"])
    }

    /// One watcher in full, with its description file (`watchers show`).
    public func watcher(_ name: String) -> Result<Components.Schemas.WatcherDetail, Failure> {
        call(Components.Schemas.WatcherDetail.self, ["watchers", "show", name])
    }

    /// What trusting a watcher approves, trusting nothing (`watchers trust --check`).
    public func watcherTrustCheck(_ name: String) -> Result<Components.Schemas.WatcherTrustOutput, Failure> {
        call(Components.Schemas.WatcherTrustOutput.self, ["watchers", "trust", name, "--check"])
    }

    /// Trusts a watcher to run, after the user reviewed `watcherTrustCheck`.
    public func trustWatcher(_ name: String) -> Result<Components.Schemas.WatcherTrustOutput, Failure> {
        call(Components.Schemas.WatcherTrustOutput.self, ["watchers", "trust", name, "--yes"])
    }

    /// Pauses a watcher (it stops), or resumes it.
    public func setWatcherPaused(_ name: String, _ paused: Bool) -> Result<Components.Schemas.WatcherChange, Failure> {
        call(Components.Schemas.WatcherChange.self, ["watchers", paused ? "pause" : "resume", name])
    }

    /// Deletes a watcher; the hooks listening to it stay (`watchers delete`).
    public func deleteWatcher(_ name: String) -> Result<Components.Schemas.WatcherChange, Failure> {
        call(Components.Schemas.WatcherChange.self, ["watchers", "delete", name])
    }
}
