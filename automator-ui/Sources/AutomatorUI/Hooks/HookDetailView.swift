import AutomatorUICore
import SwiftUI

/// One hook's window, modelled on a routine's: its state and controls on
/// top (Fire with a sample event… in place of Run Now; no schedule), then
/// Overview (status, what it acts on, what it can reach or runs), Runs (with
/// the events each was for), Memories (an LLM hook's) and Settings.
struct HookDetailView: View {
    @ObservedObject var model: HookModel
    let openWatcher: (String) -> Void
    @State private var confirmingDelete = false
    @State private var firing = false

    var body: some View {
        ScrollViewReader { scroller in
            content
                .onChange(of: model.shownRun) { scrollToShownRun(scroller) }
                .onChange(of: model.runs?.count) { scrollToShownRun(scroller) }
        }
    }

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
                if let error = model.loadError, model.detail == nil {
                    Banner(
                        title: "Couldn't read \(model.name)", message: error, systemImage: "xmark.octagon.fill",
                        tint: .red)
                }
                if let detail = model.detail, !detail.trusted {
                    Banner(title: "Not trusted", message: detail.untrusted, systemImage: "lock.shield") {
                        Button("Review…") {
                            model.tab = .overview
                            if !model.accessOpen { model.toggleAccess() }
                        }
                    }
                }
                if let watcher = model.detail?.watcher, !watcher.trusted {
                    Banner(
                        title: "Its watcher \(watcher.name) isn't trusted",
                        message: "\(model.name) hears nothing until it is.",
                        systemImage: "antenna.radiowaves.left.and.right"
                    ) {
                        Button("Review \(watcher.name)…") { openWatcher(watcher.name) }
                            .help("Open \(watcher.name): review what it runs, and trust it")
                    }
                }
                Picker("Show", selection: $model.tab) {
                    ForEach(model.tabs) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(maxWidth: 420)
                .frame(maxWidth: .infinity)
                switch model.tab {
                case .overview: HookOverview(model: model, openWatcher: openWatcher)
                case .runs: RoutineRunsView(model: model)
                case .memories: RoutineMemoriesView(model: model)
                case .settings: HookSettingsView(model: model)
                }
            }
            .padding(20)
            .frame(maxWidth: 760)
            .frame(maxWidth: .infinity)
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 600, idealWidth: 680, minHeight: 480, idealHeight: 760)
        .alert("Delete \(model.name)?", isPresented: $confirmingDelete) {
            Button("Delete", role: .destructive) { model.delete() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(HookWords.deleteHook)
        }
        .sheet(isPresented: $firing) { FireSheet(model: model) }
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
                Label("Stop", systemImage: "stop.fill")
            }
            .help("Stop the run in progress (or waiting for its turn); it's recorded as stopped")
        } else {
            Button {
                firing = true
            } label: {
                Label("Fire…", systemImage: "bolt.fill")
            }
            .help("Fire with a sample event: run it now, as a dry run or for real, as its watcher's event would")
        }
        if let enabled = model.detail?.enabled {
            Button {
                model.setPaused(enabled)
            } label: {
                Label(enabled ? "Pause" : "Resume", systemImage: enabled ? "pause.fill" : "play.circle")
            }
            .help(enabled ? "Turn it off: it no longer runs on its events" : "Turn it on: it runs on its events again")
        }
        Menu {
            Button("Delete…", role: .destructive) { confirmingDelete = true }
        } label: {
            Image(systemName: "ellipsis.circle")
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("More")
    }
}

/// Fire with a sample event: one event as JSON, as its watcher would print
/// it, run as a dry run or for real; then how the run went.
struct FireSheet: View {
    @ObservedObject var model: HookModel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Fire \(model.name) with a sample event").font(.headline)
            Text(
                "One event as its watcher would print it: an id, a type, and optionally at and data. It runs whatever its staleness and rate."
            )
            .font(.subheadline)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
            TextEditor(text: $model.sampleEvent)
                .font(.callout.monospaced())
                .autocorrectionDisabled()
                .frame(minHeight: 130)
                .padding(4)
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(Color.primary.opacity(0.15)))
            Text(HookWords.dryRun(program: model.isProgram))
                .font(.subheadline)
                .foregroundStyle(.secondary)
            result
            HStack {
                if let dryRun = model.firing {
                    ProgressView().controlSize(.small)
                    Text(dryRun ? "Dry run going…" : "Running…").font(.subheadline).foregroundStyle(.secondary)
                }
                Spacer()
                Button("Close") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                    .help("Close this; a run going carries on, and a real one shows in Runs")
                Group {
                    Button("Dry Run") { model.fire(dryRun: true) }
                        .help(HookWords.dryRun(program: model.isProgram))
                    Button("Fire") { model.fire(dryRun: false) }
                        .keyboardShortcut(.defaultAction)
                        .help("Run it for real on this event; it needs trusting")
                }
                .disabled(model.firing != nil)
            }
        }
        .padding(20)
        .frame(width: 520)
    }

    @ViewBuilder private var result: some View {
        if let error = model.fireError {
            RowError(message: error)
        } else if let output = model.fireResult {
            ScrollView {
                VStack(alignment: .leading, spacing: 8) {
                    HStack(spacing: 6) {
                        Text("How it went").font(.subheadline.weight(.semibold)).foregroundStyle(.secondary)
                        if let outcome = output.outcome { Tag(text: outcome) }
                        if output.dryRun == true { Tag(text: "dry run", tint: .blue) }
                    }
                    if let text = output.text ?? output.reason, !text.isEmpty {
                        Text(verbatim: text).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                    }
                    if let program = output.program {
                        ProgramRunView(run: program, finishedAt: nil)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: 260)
        }
    }
}

/// Status, what the hook acts on, and what it can reach or runs.
struct HookOverview: View {
    @ObservedObject var model: HookModel
    let openWatcher: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            SettingsCard { status }
            if let detail = model.detail {
                SettingsCard { HookTrigger(detail: detail, openWatcher: openWatcher) }
                SettingsCard { HookAccess(model: model, detail: detail) }
            }
        }
    }

    @ViewBuilder private var status: some View {
        CardTitle("Status", systemImage: "waveform.path.ecg")
        Divider().padding(.leading, SettingsMetrics.rowInset)
        if let problem = model.entry?.problem ?? model.detail?.problem, !problem.isEmpty {
            InfoRow(label: "Can't run") { RowError(message: problem) }
        }
        InfoRow(label: "Last run") {
            if let last = model.entry?.lastRun ?? model.detail?.lastRun {
                HStack(spacing: 6) {
                    Text(Formats.when(last.startedAt))
                    OutcomeTag(outcome: last.outcome.rawValue)
                    if let trigger = last.trigger, !trigger.isEmpty { Tag(text: trigger) }
                    Spacer(minLength: 0)
                    if let id = last.id {
                        Button("Show Run") { model.showRun(id) }
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
        .padding(.bottom, 4)
    }
}

/// What a hook acts on (its trigger): its watcher, with its state and a
/// link to it, the event type, batches, staleness and rate.
struct HookTrigger: View {
    let detail: HookDetail
    let openWatcher: (String) -> Void

    var body: some View {
        CardTitle("What it acts on", systemImage: "bolt")
        Divider().padding(.leading, SettingsMetrics.rowInset)
        InfoRow(label: "Watcher") {
            if let watcher = detail.watcher {
                HStack(spacing: 6) {
                    Button(watcher.name) { openWatcher(watcher.name) }
                        .buttonStyle(.link)
                        .help("Open \(watcher.name)")
                    WatcherTags(trusted: watcher.trusted, enabled: watcher.enabled, error: watcher.error)
                }
                Text(watcher.modeText).font(.subheadline).foregroundStyle(.secondary)
                if let error = watcher.error, !error.isEmpty {
                    RowError(message: error)
                    if let at = watcher.errorAt {
                        Text("Since \(Formats.when(at))").font(.subheadline).foregroundStyle(.secondary)
                    }
                }
            } else {
                Text(detail.on.watcher).font(.callout.monospaced())
                Text("There's no watcher by that name.").font(.subheadline).foregroundStyle(.red)
            }
        }
        InfoRow(label: "Events") {
            if let type = detail.on._type, !type.isEmpty {
                Text(verbatim: type).font(.callout.monospaced())
            } else {
                Text("Every type")
            }
        }
        if !detail.isProgram {
            InfoRow(label: "Batches") { Text(HookWords.batch(detail.batch ?? false)) }
        }
        InfoRow(label: "Staleness") { Text(HookWords.staleness(detail.staleness)) }
        InfoRow(label: "Rate") { Text(HookWords.rate(detail.rate)) }
            .padding(.bottom, 4)
    }
}

/// A watcher's state as tags: trusted, paused, failing.
struct WatcherTags: View {
    let trusted: Bool
    let enabled: Bool
    let error: String?

    var body: some View {
        if trusted {
            Tag(text: "Trusted", tint: .green)
        } else {
            Tag(text: "Not trusted", tint: .orange)
        }
        if !enabled { Tag(text: "Paused") }
        if let error, !error.isEmpty { Tag(text: "Failing", tint: .red) }
    }
}

/// What a hook can reach (an LLM hook's servers and tools) or runs (a
/// program hook's executable), with what it acts on, which trusting it
/// covers too; warnings before Trust (or Turn On, for one that's off).
struct HookAccess: View {
    @ObservedObject var model: HookModel
    let detail: HookDetail

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
            if let warnings = model.trustReview?.warnings, !warnings.isEmpty {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(warnings, id: \.self) { warning in
                        Banner(
                            title: "Before you trust it", message: warning, systemImage: "exclamationmark.triangle.fill"
                        )
                    }
                }
                .padding(.horizontal, SettingsMetrics.rowInset)
                .padding(.vertical, 8)
            }
            InfoRow(label: "Acts on") {
                Text(
                    (model.trustReview?.trigger.map { Components.Schemas.On(_type: $0._type, watcher: $0.watcher) }
                        ?? detail.on).text)
                Text("Trusting it covers this too: another watcher or event type needs trusting again.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let error = model.trustReview?.toolsError, !error.isEmpty {
                InfoRow(label: "Tools") { RowError(message: error) }
            }
            review
        }
    }

    private var review: TrustReviewView {
        let surface = model.trustReview?.surface
        let turnOn = !detail.enabled
        let trust: (() -> Void)? = detail.trusted ? nil : { [model] in model.trust(turnOn: turnOn) }
        var view = TrustReviewView(
            servers: surface?.servers ?? detail.servers ?? [], tools: surface?.tools ?? detail.tools ?? [],
            builtinTools: surface?.builtinTools ?? detail.builtinTools, folder: surface?.folder ?? detail.folder,
            program: model.trustReview?.program ?? detail.program,
            review: model.trustReview, error: model.trustError, trust: trust, working: model.working)
        if turnOn {
            view.actionTitle = "Turn On"
            view.note =
                "Turning it on trusts it with what's listed above and has it run on its events. Dry runs never need it."
            view.programNote = "Turning it on trusts this executable (dry runs included) and has it run on its events."
        } else {
            view.note =
                "Trusting it lets it run for real on its events with what's listed above. Dry runs never need it."
            view.programNote = "Trusting it lets this executable run on its events, dry runs included."
        }
        view.signIn = SignInAction(working: model.signingIn, error: model.signInError) { [model] in model.signIn() }
        return view
    }
}

/// The hook's own settings, as rows like the app's settings, and its file
/// (read-only).
struct HookSettingsView: View {
    @ObservedObject var model: HookModel

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            SettingsCard {
                CardTitle("Settings", systemImage: "slider.horizontal.3")
                SettingRows(entries: entries, errors: model.errors, set: model.set, reset: model.reset)
            }
            if let detail = model.detail {
                SettingsCard {
                    ProgramFile(
                        title: detail.isProgram ? "Program file" : "Prompt file", description: detail.description,
                        path: detail.path, folder: detail.isProgram ? detail.folder : nil, source: detail.source,
                        update: "\(AppInfo.command) hooks update \(detail.name) --from <file>")
                }
            }
        }
    }

    /// The settings labelled without "hooks.<name>.".
    private var entries: [KeyTree.Entry<SettingState>] {
        KeyTree.build(model.settings) { $0.key }
            .flatMap { group in group.entries + group.groups.flatMap(\.entries) }
    }
}
