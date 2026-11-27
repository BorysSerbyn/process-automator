import AutomatorUICore
import SwiftUI

/// One routine's window: its state and controls on top, then Overview
/// (status, schedule, what it can reach), Runs, Memories and Settings. A
/// program's has what it runs in place of what it can reach, and no Memories.
struct RoutineDetailView: View {
    @ObservedObject var model: RoutineModel
    @State private var confirmingDelete = false

    var body: some View {
        ScrollViewReader { scroller in
            content
                .onChange(of: model.shownRun) { scrollToShownRun(scroller) }
                .onChange(of: model.runs?.count) { scrollToShownRun(scroller) }
        }
    }

    /// Brings the run shown from a notification into view, once it's listed.
    private func scrollToShownRun(_ scroller: ScrollViewProxy) {
        guard let id = model.shownRun, model.tab == .runs, model.runs?.contains(where: { $0.id == id }) == true
        else { return }
        model.shownRun = nil
        DispatchQueue.main.async {
            withAnimation { scroller.scrollTo(id, anchor: .top) }
        }
    }

    private var content: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                header
                if let error = model.actionError { RowError(message: error) }
                ExportLogsStatus(report: model.bugReport)
                if let error = model.loadError, model.detail == nil {
                    Banner(
                        title: "Couldn't read \(model.name)", message: error, systemImage: "xmark.octagon.fill",
                        tint: .red)
                }
                if let detail = model.detail {
                    PermissionsBanner(name: model.name, permissions: detail.permissions ?? [], granter: model.granter)
                }
                Picker("Show", selection: $model.tab) {
                    ForEach(model.tabs) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(maxWidth: 420)
                .frame(maxWidth: .infinity)
                switch model.tab {
                case .overview: RoutineOverview(model: model)
                case .runs: RoutineRunsView(model: model)
                case .memories: RoutineMemoriesView(model: model)
                case .settings: RoutineSettingsView(model: model)
                }
            }
            .padding(20)
            .frame(maxWidth: 760)
            .frame(maxWidth: .infinity)
            // Not on the window's other sheets' views: two sheets on one view clash.
            .modifier(BugReportPresenter(report: model.bugReport))
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 600, idealWidth: 680, minHeight: 480, idealHeight: 760)
        .modifier(TurnOnPresenter(turnOn: model.turnOn, summary: turnOnSummary))
        .alert("Delete \(model.name)?", isPresented: $confirmingDelete) {
            Button("Delete", role: .destructive) { model.delete() }
            Button("Cancel", role: .cancel) {}
        } message: {
            if model.isProgram {
                Text("Its copy, settings, trust and secrets are deleted. Its run history and data folder are kept.")
            } else {
                Text("Its file, settings, trust and memories are deleted. Its run history is kept.")
            }
        }
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 12) {
            HealthDot(health: model.entry?.health, size: 12)
            VStack(alignment: .leading, spacing: 2) {
                Text(model.name).font(.title2.weight(.semibold))
                if let summary = model.entry?.summary {
                    Text(summary)
                        .font(.subheadline)
                        .foregroundStyle(model.entry?.health == "error" ? Color.red : Color.secondary)
                }
            }
            Spacer(minLength: 12)
            controls.disabled(model.working || model.detail == nil)
        }
    }

    @ViewBuilder private var controls: some View {
        if model.running {
            Button(role: .destructive, action: model.stop) {
                PendingLabel(pending: model.pending, actions: ["stop"], title: "Stop", systemImage: "stop.fill")
            }
            .help("Stop the run in progress (or waiting for its turn); it's recorded as stopped")
        } else {
            Menu {
                Button("Dry Run") { model.runNow(dryRun: true) }
                    .help(
                        model.detail?.program?.dryRunText
                            ?? "Run it with every write intercepted: nothing is posted or changed")
                if let program = model.detail?.program {
                    // What a program's dry run is, under it: it depends on whether it writes.
                    Divider()
                    Text(program.dryRunText)
                    Text(ProgramInfo.dryRunTrust)
                }
            } label: {
                PendingLabel(
                    pending: model.pending, actions: ["run", "dry-run"], title: "Run Now", systemImage: "play.fill")
            } primaryAction: {
                model.runNow(dryRun: false)
            }
            .fixedSize()
            .help(runHelp)
        }
        if let detail = model.detail {
            if detail.turnOn == true {
                Button(action: model.startTurnOn) {
                    Label("Turn On…", systemImage: "power")
                }
                .buttonStyle(.borderedProminent)
                .help("Review what it can reach, then turn it on")
            } else if detail.enabled {
                Button {
                    model.setPaused(true)
                } label: {
                    PendingLabel(pending: model.pending, actions: ["pause"], title: "Pause", systemImage: "pause.fill")
                }
                .help("Stop running it on its schedule")
            }
        }
        Menu {
            if model.detail?.turnOn == false, model.detail?.enabled == false {
                Button("Resume") { model.setPaused(false) }
            }
            Button("Export Logs…") { Task { await model.bugReport.exportLogs() } }
                .help("Save its latest runs' logs as a zip, secrets redacted, and show it in Finder")
            Button("Report a Bug…", action: model.bugReport.start)
                .help("Open a new issue on GitHub about it, filled in, to submit from your own account")
            Divider()
            Button("Delete…", role: .destructive) { confirmingDelete = true }
        } label: {
            Image(systemName: "ellipsis.circle")
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("More")
    }

    /// What it does, in a line, for its Turn On review.
    private var turnOnSummary: String? {
        guard let detail = model.detail else { return nil }
        let when = detail.scheduleText.map { "Runs: \($0)." }
        return [detail.description, when].compactMap { $0 }.joined(separator: " ")
    }

    private var runHelp: String {
        guard let program = model.detail?.program else {
            return "Run it now, in the background. The arrow offers a dry run."
        }
        return "Run it now, in the background. The arrow offers a dry run: \(program.dryRunText)"
    }
}

/// Status, schedule and what the routine can reach.
struct RoutineOverview: View {
    @ObservedObject var model: RoutineModel

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            SettingsCard { status }
            if let setting = model.scheduleSetting {
                SettingsCard {
                    ScheduleEditor(
                        setting: setting, current: model.schedule, cli: model.cli, error: model.errors[setting.key],
                        save: { model.set(setting.key, $0) }, reset: { model.reset(setting.key) })
                }
            }
            if let detail = model.detail {
                SettingsCard { RoutineAccess(model: model, detail: detail) }
                PermissionsCard(permissions: detail.permissions ?? [], granter: model.granter)
            }
        }
    }

    @ViewBuilder private var status: some View {
        CardTitle("Status", systemImage: "waveform.path.ecg")
        Divider().padding(.leading, SettingsMetrics.rowInset)
        if let problem = model.entry?.problem, !problem.isEmpty {
            InfoRow(label: "Can't schedule") { RowError(message: problem) }
        }
        InfoRow(label: "Last run") {
            if let last = model.entry?.lastRun ?? model.detail?.lastRun {
                HStack(spacing: 6) {
                    Text(Formats.when(last.startedAt))
                    OutcomeTag(outcome: last.outcome.rawValue)
                    if let trigger = last.trigger, !trigger.isEmpty { Tag(text: trigger) }
                    Spacer(minLength: 0)
                    if let id = last.id {
                        Button("Show Run") {
                            model.tab = .runs
                            if !model.openRuns.contains(id) { model.toggleRun(id) }
                        }
                        .buttonStyle(.link)
                        .font(.subheadline)
                    }
                }
                if let reason = last.reason, !reason.isEmpty {
                    Text(reason)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                }
            } else {
                Text("Never").foregroundStyle(.secondary)
            }
        }
        InfoRow(label: "Next run") {
            if let next = model.entry?.nextRunAt ?? model.detail?.next {
                Text(Formats.when(next))
            } else {
                Text("None").foregroundStyle(.secondary)
            }
        }
        .padding(.bottom, 4)
    }
}

/// What a routine can reach (its trust surface), and the Trust button when
/// it isn't trusted.
struct RoutineAccess: View {
    @ObservedObject var model: RoutineModel
    let detail: RoutineDetail

    var body: some View {
        DisclosureRow(
            open: model.accessOpen, help: detail.isProgram ? "what it runs" : "what it can reach",
            toggle: model.toggleAccess
        ) {
            Image(systemName: "lock.shield").foregroundStyle(.secondary).frame(width: 16)
            Text(detail.isProgram ? "What it runs" : "What it can reach").font(.headline)
            Spacer(minLength: 8)
            if detail.trusted {
                Tag(text: "Trusted", tint: .green)
            } else {
                Tag(text: "Not trusted", tint: .orange)
            }
        }
        if model.accessOpen {
            Divider().padding(.leading, SettingsMetrics.rowInset)
            review
        }
    }

    /// The review's surface once it's read, else the routine's own; read
    /// only: Turn On… is where it's trusted. A program's secrets that aren't
    /// stored can be signed in to here.
    private var review: TrustReviewView {
        let surface = model.trustReview?.surface
        var view = TrustReviewView(
            servers: surface?.servers ?? detail.servers, tools: surface?.tools ?? detail.tools,
            builtinTools: surface?.builtinTools ?? detail.builtinTools, folder: surface?.folder ?? detail.folder,
            program: model.trustReview?.program ?? detail.program,
            review: model.trustReview, error: model.trustError, trust: nil, working: model.working)
        view.signIn = SignInAction(working: model.signingIn != nil, error: model.signInError) { [model] in
            model.signIn(model.name)
        }
        return view
    }
}

/// What a routine can reach (its trust surface): servers, tools (writes
/// tinted), built-in tools, folder, and what changed since it was trusted;
/// with a Trust button when `trust` is given.
struct TrustReviewView: View {
    let servers: [String]
    let tools: [String]
    let builtinTools: [String]
    let folder: String?
    /// A program's: what runs, in place of servers and tools.
    let program: ProgramInfo?
    let review: TrustReview?
    let error: String?
    let trust: (() -> Void)?
    var working = false
    /// The button that does it, and the words beside it (none: the question
    /// is asked elsewhere, as setup does).
    var actionTitle = "Trust"
    var note: String? = "Trusting it lets it run for real with what's listed above. Dry runs never need it."
    /// The words beside a program's button.
    var programNote = "Trusting it lets this executable run, dry runs included."
    /// A program's sign-in to its secrets that aren't stored (none: setup has
    /// its own sign-in step).
    var signIn: SignInAction?

    init(
        servers: [String], tools: [String], builtinTools: [String]?, folder: String?, program: ProgramInfo? = nil,
        review: TrustReview?, error: String?, trust: (() -> Void)?, working: Bool = false
    ) {
        self.servers = servers
        self.tools = tools
        self.builtinTools = builtinTools ?? []
        self.folder = folder
        self.program = program
        self.review = review
        self.error = error
        self.trust = trust
        self.working = working
    }

    /// The review's surface, for a routine known only by name (setup).
    init(
        review: TrustReview, error: String?, trust: (() -> Void)?, working: Bool = false, actionTitle: String,
        note: String?
    ) {
        self.init(
            servers: review.surface.servers, tools: review.surface.tools, builtinTools: review.surface.builtinTools,
            folder: review.surface.folder, program: review.program, review: review, error: error, trust: trust,
            working: working)
        self.actionTitle = actionTitle
        self.note = note
    }

    var body: some View {
        if let program {
            ProgramRows(program: program, signIn: signIn)
        } else {
            InfoRow(label: "Servers") { names(servers) }
            InfoRow(label: "Tools") {
                names(tools, marked: writes)
                if !writes.isEmpty {
                    Text("Tinted tools write (they're intercepted in dry runs).")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
            }
            if !builtinTools.isEmpty {
                InfoRow(label: "Built-in tools") { names(builtinTools) }
            }
        }
        if program == nil, let folder, !folder.isEmpty {
            InfoRow(label: "Folder") {
                Text(folder).font(.callout.monospaced()).textSelection(.enabled)
            }
        }
        if let changes = review?.changes, !changes.isEmpty {
            InfoRow(label: "Changed") {
                ForEach(changes, id: \.self) { change in
                    Label(change, systemImage: "arrow.triangle.2.circlepath")
                        .font(.subheadline)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
        if let error {
            RowError(message: error)
                .padding(.horizontal, SettingsMetrics.rowInset)
                .padding(.bottom, 8)
        }
        if let trust {
            Divider().padding(.leading, SettingsMetrics.rowInset)
            HStack(spacing: 12) {
                if let note = program == nil ? note : programNote {
                    Text(note)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 8)
                Button(actionTitle, action: trust)
                    .keyboardShortcut(.defaultAction)
                    .disabled(working || review == nil)
            }
            .padding(.horizontal, SettingsMetrics.rowInset)
            .padding(.vertical, 12)
        }
    }

    /// Tools that write, as the trust review marks them (not read-only).
    private var writes: Set<String> {
        Set((review?.tools ?? []).filter { !$0.readOnly }.map(\.name))
    }

    @ViewBuilder private func names(_ list: [String], marked: Set<String> = []) -> some View {
        if list.isEmpty {
            Text("None").foregroundStyle(.secondary)
        } else {
            Chips(names: list, marked: marked)
        }
    }
}

/// The routine's own settings (all but its schedule, which Overview edits),
/// as rows like the app's settings, and where its file is.
struct RoutineSettingsView: View {
    @ObservedObject var model: RoutineModel

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            SettingsCard {
                CardTitle("Settings", systemImage: "slider.horizontal.3")
                SettingRows(entries: entries, errors: model.errors, set: model.set, reset: model.reset)
            }
            if let detail = model.detail {
                SettingsCard {
                    if detail.isProgram {
                        ProgramFile(detail: detail)
                    } else {
                        CardTitle("Routine file", systemImage: "doc.text")
                        Divider().padding(.leading, SettingsMetrics.rowInset)
                        InfoRow(label: "Model") {
                            Text(detail.agentModel ?? detail.model ?? "The backend's default")
                                .foregroundStyle(detail.agentModel ?? detail.model == nil ? .secondary : .primary)
                        }
                        InfoRow(label: "File") { PathText(path: detail.path) }
                            .padding(.bottom, 4)
                    }
                }
            }
        }
    }

    /// The settings with their labels: the key without "routines.<name>."
    /// (KeyTree puts them two groups deep).
    private var entries: [KeyTree.Entry<SettingState>] {
        KeyTree.build(model.otherSettings) { $0.key }
            .flatMap { group in group.entries + group.groups.flatMap(\.entries) }
    }
}

/// A path, in monospace, selectable and wrapping.
struct PathText: View {
    let path: String

    var body: some View {
        Text(path)
            .font(.callout.monospaced())
            .textSelection(.enabled)
            .fixedSize(horizontal: false, vertical: true)
    }
}

/// A description file (a program's, a hook's, a watcher's), as the app keeps
/// it: what it says it does, where it is, its data folder, and its text
/// (read-only: it's updated from a terminal, with `update`).
struct ProgramFile: View {
    var title = "Program file"
    let description: String?
    let path: String
    var folder: String?
    let source: String
    /// The command that updates it (`<command> routines update`).
    var update = "\(AppInfo.command) routines update"

    init(detail: RoutineDetail) {
        self.init(description: detail.description, path: detail.path, folder: detail.folder, source: detail.source)
    }

    init(
        title: String = "Program file", description: String?, path: String, folder: String? = nil, source: String,
        update: String = "\(AppInfo.command) routines update"
    ) {
        self.title = title
        self.description = description
        self.path = path
        self.folder = folder
        self.source = source
        self.update = update
    }

    var body: some View {
        CardTitle(title, systemImage: "doc.text")
        Divider().padding(.leading, SettingsMetrics.rowInset)
        if let description, !description.isEmpty {
            InfoRow(label: "Description") {
                Text(description).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
            }
        }
        InfoRow(label: "File") { PathText(path: path) }
        if let folder, !folder.isEmpty {
            InfoRow(label: "Data folder") {
                PathText(path: folder)
                Text("Where it works: kept between runs, and when it's deleted.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
        }
        VStack(alignment: .leading, spacing: 6) {
            Text(verbatim: source.isEmpty ? " " : source)
                .font(.callout.monospaced())
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                .padding(12)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(
                    Color.primary.opacity(0.035), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            Text("Read-only: the app's copy. Update it from a terminal (`\(update)`).")
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.horizontal, SettingsMetrics.rowInset)
        .padding(.top, 4)
        .padding(.bottom, 12)
    }
}

/// Signing in to a program's secrets (`auth login <program> --browser`):
/// what starts it, whether it's waiting for me, and why it failed.
struct SignInAction {
    var working: Bool
    var error: String?
    var start: () -> Void
}

/// What a program runs, as trust approves it: its executable (the app's
/// copy, by checksum: shortened, copied whole), whether it writes, its
/// arguments, and its secrets (each with the variable it's passed as and
/// whether it's stored; Sign In… when one isn't).
struct ProgramRows: View {
    let program: ProgramInfo
    var signIn: SignInAction?
    /// Whether it says if it writes (a watcher only reads: it doesn't).
    var showsWrites = true

    var body: some View {
        InfoRow(label: "Executable") {
            PathText(path: program.executable)
            if let short = program.shortChecksum, let checksum = program.checksum {
                HStack(spacing: 4) {
                    Text("SHA-256 \(short)")
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                        .help(checksum)
                    Button {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(checksum, forType: .string)
                    } label: {
                        Image(systemName: "doc.on.doc")
                    }
                    .buttonStyle(.borderless)
                    .controlSize(.small)
                    .help("Copy the whole SHA-256")
                }
            }
        }
        if showsWrites {
            InfoRow(label: "Writes") {
                Text(program.writes ? "Yes: a dry run passes it --dry-run" : "No: a dry run runs it as it is")
            }
        }
        if !program.args.isEmpty {
            InfoRow(label: "Arguments") {
                Text(program.args.joined(separator: " ")).font(.callout.monospaced()).textSelection(.enabled)
            }
        }
        if !program.secrets.isEmpty {
            InfoRow(label: "Secrets") {
                ForEach(program.secrets, id: \.name) { secret in
                    HStack(spacing: 6) {
                        Text(secret.name).font(.callout.monospaced())
                        Text("as $\(secret.env)").foregroundStyle(.secondary)
                        if secret.stored {
                            Tag(text: "Stored", tint: .green)
                        } else {
                            Tag(text: "Not stored", tint: .orange)
                        }
                    }
                }
                if let signIn, program.needsSignIn {
                    signInRow(signIn)
                }
            }
        }
    }

    @ViewBuilder private func signInRow(_ signIn: SignInAction) -> some View {
        HStack(spacing: 8) {
            if signIn.working {
                ProgressView().controlSize(.small)
                Text("Enter them on the page in your browser…").font(.subheadline).foregroundStyle(.secondary)
            } else {
                Button("Sign In…", action: signIn.start)
                    .help("Enter its secrets on a page in your browser; they're stored in the Keychain")
                Text("Opens your browser to enter them.").font(.subheadline).foregroundStyle(.secondary)
            }
        }
        .padding(.top, 2)
        if let error = signIn.error { RowError(message: error) }
    }
}
