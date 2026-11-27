import Combine
import Foundation

public typealias LogsOutput = Components.Schemas.LogsOutput
public typealias ReportOutput = Components.Schemas.ReportOutput

extension CLI {
    // MARK: Logs and bug reports

    /// `<routines|hooks> logs <name>`: the zip of its latest runs' logs.
    public static func logsArguments(_ name: String, kind: TurnOn.Kind) -> [String] {
        [kind.group, "logs", name]
    }

    /// `<routines|hooks> report <name>`: its bug report's new-issue page.
    /// The title and description are given as `--flag=value`, so text
    /// starting with "-" isn't read as a flag.
    public static func reportArguments(
        _ name: String, kind: TurnOn.Kind, title: String, description: String, includeLogs: Bool, dryRun: Bool
    ) -> [String] {
        [kind.group, "report", name, "--title=\(title)", "--description=\(description)"]
            + (includeLogs ? ["--include-logs"] : []) + (dryRun ? ["--dry-run"] : [])
    }

    /// Exports a routine's or hook's latest runs' logs as a zip in the app's
    /// reports folder, secrets redacted (`routines logs`, `hooks logs`).
    public func exportLogs(_ name: String, kind: TurnOn.Kind) -> Result<LogsOutput, Failure> {
        call(LogsOutput.self, Self.logsArguments(name, kind: kind))
    }

    /// A bug report on a routine or hook (`routines report`, `hooks report`):
    /// the core opens its new-issue page in the browser, filled in, and shows
    /// the logs (with `includeLogs`) in Finder. A dry run opens nothing.
    public func report(
        _ name: String, kind: TurnOn.Kind, title: String, description: String, includeLogs: Bool, dryRun: Bool
    ) -> Result<ReportOutput, Failure> {
        call(
            ReportOutput.self,
            Self.reportArguments(
                name, kind: kind, title: title, description: description, includeLogs: includeLogs, dryRun: dryRun))
    }
}

extension TurnOn.Kind {
    /// Its commands' group: `routines` or `hooks`.
    var group: String { self == .hook ? "hooks" : "routines" }
}

/// Export Logs… and Report a Bug… for a routine or hook. Export Logs writes
/// the zip and shows it in Finder. Report a Bug… is a sheet: where it goes
/// is read first (a dry run, which opens nothing), then Open on GitHub has
/// the core open the issue, filled in, to file from my own account, and the
/// logs, when attached, in Finder to drag onto it. Every failure is kept
/// for the window or the sheet to show.
@MainActor
public final class BugReport: ObservableObject {
    public let name: String
    public let kind: TurnOn.Kind
    let cli: CLI
    /// Shows a file in Finder; the app's is NSWorkspace's.
    public var reveal: (URL) -> Void = { _ in }

    /// Export Logs is waiting for the core, and why it failed.
    @Published public private(set) var exporting = false
    @Published public private(set) var exportError: String?

    /// Whether the sheet shows.
    @Published public var showing = false
    @Published public var title = ""
    @Published public var description = ""
    @Published public var includeLogs = false
    /// The GitHub repository it goes to (owner/name), read when the sheet opens.
    @Published public private(set) var repo: String?
    /// Why it can't be reported: the app takes no bug reports, say.
    @Published public private(set) var loadError: String?
    /// Open on GitHub is waiting for the core, and why it failed.
    @Published public private(set) var working = false
    @Published public private(set) var error: String?
    /// The issue was opened with logs: the zip's file name, to drag onto it.
    @Published public private(set) var logsToDrag: String?
    /// The issue's page, when the browser didn't open it.
    @Published public private(set) var unopened: URL?

    public init(name: String, kind: TurnOn.Kind, cli: CLI) {
        self.name = name
        self.kind = kind
        self.cli = cli
    }

    /// Export Logs…: writes the zip, then shows it in Finder.
    public func exportLogs() async {
        exporting = true
        exportError = nil
        let result = await cli.background { [name, kind] in $0.exportLogs(name, kind: kind) }
        exporting = false
        switch result {
        case .success(let logs): reveal(URL(filePath: logs.path))
        case .failure(let failure): exportError = failure.message
        }
    }

    /// Report a Bug…: the sheet shows, afresh, and where it goes is read.
    public func start() {
        error = nil
        logsToDrag = nil
        unopened = nil
        showing = true
        Task { await load() }
    }

    /// Reads where the report goes (`report --dry-run`: it opens nothing and,
    /// without logs, writes nothing).
    public func load() async {
        loadError = nil
        let result = await cli.background { [name, kind] in
            $0.report(name, kind: kind, title: "", description: "", includeLogs: false, dryRun: true)
        }
        switch result {
        case .success(let report): repo = report.repo
        case .failure(let failure): loadError = failure.message
        }
    }

    /// Open on GitHub: the core opens the issue (and the logs in Finder).
    /// With logs, the sheet stays to say to drag them onto it; else it closes.
    public func open() async {
        working = true
        error = nil
        let result = await cli.background {
            [name, kind, title, description, includeLogs] in
            $0.report(
                name, kind: kind, title: title, description: description, includeLogs: includeLogs, dryRun: false)
        }
        working = false
        switch result {
        case .success(let report):
            repo = report.repo
            // The browser didn't open: its address, to open from the sheet.
            unopened = report.opened ? nil : URL(string: report.url)
            logsToDrag = report.logs.map { URL(filePath: $0.path).lastPathComponent }
            if unopened == nil, logsToDrag == nil { finish() }
        case .failure(let failure):
            error = failure.message
        }
    }

    /// Done, after the issue was opened: the sheet closes, emptied.
    public func finish() {
        showing = false
        title = ""
        description = ""
        includeLogs = false
        logsToDrag = nil
        unopened = nil
    }

    /// Who files it and who sees it.
    public var whereText: String? {
        repo.map {
            "It's filed from your own GitHub account on \($0), and it's public if that repository is."
        }
    }

    /// What attaching the logs shares.
    public static let logsNote =
        "The logs may hold its file's text and what its runs read and wrote (anything that looks like a secret is redacted). Read them before you attach them."

    /// What to do once the issue is open with logs.
    public var dragText: String? {
        logsToDrag.map { "Drag \($0) from the Finder window onto the issue before you submit it." }
    }
}
