import AutomatorUICore
import SwiftUI

/// One watcher as its window shows it (`watchers show`): how it runs, what
/// it runs, its trust, the hooks listening and how it last went. Every
/// change goes through the CLI, then it's re-read.
@MainActor
final class WatcherModel: ObservableObject {
    let name: String
    let cli: CLI
    @Published var detail: WatcherDetail?
    @Published var loadError: String?
    @Published var status: RunStatus?
    @Published var review: WatcherReview?
    @Published var trustError: String?
    @Published var signingIn = false
    @Published var signInError: String?
    @Published var actionError: String?
    @Published var working = false
    var onDeleted: () -> Void = {}
    /// Grant… for the permissions it declares that aren't allowed.
    let granter: PermissionGranter
    private var watchingPermissions = false

    init(name: String, cli: CLI, status: RunStatus?) {
        self.name = name
        self.cli = cli
        self.status = status
        granter = PermissionGranter(cli: cli, app: AppInfo.name)
        granter.openURL = { NSWorkspace.shared.open($0) }
        granter.asked = { [weak self] in self?.load() }
    }

    /// Reads it again every few seconds while a permission it declares isn't
    /// allowed (the scheduler records it within a pass).
    private func watchPermissions() {
        guard !watchingPermissions else { return }
        watchingPermissions = true
        Task { [weak self] in
            while true {
                try? await Task.sleep(for: .seconds(3))
                // A window closed meanwhile is let go: it's held only weakly.
                guard let self else { return }
                guard !(self.detail?.permissions ?? []).blocking.isEmpty else {
                    self.watchingPermissions = false
                    return
                }
                self.load()
            }
        }
    }

    func load() {
        Task {
            switch await cli.background({ [name] in $0.watcher(name) }) {
            case .success(let detail):
                granter.saw(detail.permissions ?? [])
                self.detail = detail
                if !(detail.permissions ?? []).blocking.isEmpty { watchPermissions() }
                loadError = nil
                if !detail.trusted, review == nil { loadTrust() }
            case .failure(let failure):
                loadError = failure.message
            }
        }
    }

    /// A new run status: the hooks listening may have changed (a hook's
    /// problem is its watcher's state), so it's read again when they did.
    func update(_ status: RunStatus?) {
        let shape = { (s: RunStatus?) in s?.hooks?.map { [$0.name, $0.problem ?? "", $0.lastRun?.id ?? ""] } }
        let changed = shape(self.status) != shape(status)
        self.status = status
        if changed { load() }
    }

    func loadTrust() {
        Task {
            switch await cli.background({ [name] in $0.watcherTrustCheck(name) }) {
            case .success(let review):
                self.review = review
                trustError = nil
            case .failure(let failure):
                trustError = failure.message
            }
        }
    }

    func trust() {
        working = true
        Task {
            let result = await cli.background { [name] in $0.trustWatcher(name) }
            working = false
            switch result {
            case .success(let review):
                self.review = review
                trustError = nil
                load()
            case .failure(let failure):
                trustError = failure.message
            }
        }
    }

    func signIn() {
        signingIn = true
        Task {
            let failure = await cli.background { [name] in $0.signIn(name) }
            signingIn = false
            signInError = failure?.message
            load()
            if review != nil { loadTrust() }
        }
    }

    func setPaused(_ paused: Bool) {
        control({ [name] in $0.setWatcherPaused(name, paused).map { _ in () } }, then: load)
    }

    func delete() {
        control({ [name] in $0.deleteWatcher(name).map { _ in () } }, then: { self.onDeleted() })
    }

    private func control(
        _ work: @escaping @Sendable (CLI) -> Result<Void, CLI.Failure>, then: @escaping () -> Void
    ) {
        working = true
        Task {
            let result = await cli.background(work)
            working = false
            switch result {
            case .success:
                actionError = nil
                then()
            case .failure(let failure):
                actionError = failure.message
            }
        }
    }
}

/// A watcher's window: its state and Pause/Resume and Delete… on top, then
/// how it runs and last went, the hooks listening, what it runs (with Trust
/// while it isn't trusted, and Sign In… for its secrets) and its file.
struct WatcherView: View {
    @ObservedObject var model: WatcherModel
    let openHook: (String) -> Void
    @State private var confirmingDelete = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                header
                if let error = model.actionError { RowError(message: error) }
                if let error = model.loadError, model.detail == nil {
                    Banner(
                        title: "Couldn't read \(model.name)", message: error, systemImage: "xmark.octagon.fill",
                        tint: .red)
                }
                if let detail = model.detail {
                    if !detail.trusted {
                        Banner(
                            title: "Not trusted",
                            message: [
                                detail.untrusted,
                                "It doesn't run, and the hooks listening hear nothing, until it's trusted.",
                            ]
                            .compactMap { $0 }.joined(separator: ". "),
                            systemImage: "lock.shield")
                    }
                    PermissionsBanner(name: model.name, permissions: detail.permissions ?? [], granter: model.granter)
                    SettingsCard { state(detail) }
                    PermissionsCard(permissions: detail.permissions ?? [], granter: model.granter)
                    SettingsCard { hooks(detail) }
                    SettingsCard { access(detail) }
                    SettingsCard {
                        ProgramFile(
                            title: "Watcher file", description: detail.description, path: detail.path,
                            source: detail.source,
                            update: "\(AppInfo.command) watchers update \(detail.name) --from <folder>/watcher.yaml")
                    }
                }
            }
            .padding(20)
            .frame(maxWidth: 760)
            .frame(maxWidth: .infinity)
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 560, idealWidth: 640, minHeight: 420, idealHeight: 680)
        .alert("Delete \(model.name)?", isPresented: $confirmingDelete) {
            Button("Delete", role: .destructive) { model.delete() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(HookWords.deleteWatcher)
        }
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 12) {
            Image(systemName: "antenna.radiowaves.left.and.right")
                .font(.title2)
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 2) {
                Text(model.name).font(.title2.weight(.semibold))
                if let detail = model.detail {
                    HStack(spacing: 6) {
                        Text(detail.modeText).font(.subheadline).foregroundStyle(.secondary)
                        WatcherTags(trusted: detail.trusted, enabled: detail.enabled, error: detail.error)
                    }
                }
            }
            Spacer(minLength: 12)
            Group {
                if let enabled = model.detail?.enabled {
                    Button {
                        model.setPaused(enabled)
                    } label: {
                        Label(enabled ? "Pause" : "Resume", systemImage: enabled ? "pause.fill" : "play.circle")
                    }
                    .help(enabled ? "Stop it: the hooks listening hear nothing until it's resumed" : "Run it again")
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
            .disabled(model.working || model.detail == nil)
        }
    }

    @ViewBuilder private func state(_ detail: WatcherDetail) -> some View {
        CardTitle("How it runs", systemImage: "waveform.path.ecg")
        Divider().padding(.leading, SettingsMetrics.rowInset)
        InfoRow(label: "Mode") {
            Text(detail.modeText)
            if detail.mode == .poll, let timeout = detail.timeout {
                Text("A poll may take \(timeout) before it's stopped.").font(.subheadline).foregroundStyle(.secondary)
            }
        }
        if detail.mode == .poll {
            InfoRow(label: "Last poll") { when(detail.lastPollAt) }
        } else {
            InfoRow(label: "Started") { when(detail.startedAt) }
        }
        InfoRow(label: "Last event") { when(detail.lastEventAt) }
        if let error = detail.error, !error.isEmpty {
            InfoRow(label: "Failing") {
                RowError(message: error)
                if let at = detail.errorAt {
                    Text("Since \(Formats.when(at))").font(.subheadline).foregroundStyle(.secondary)
                }
            }
        }
        Spacer().frame(height: 4)
    }

    @ViewBuilder private func when(_ date: Date?) -> some View {
        if let date {
            Text(Formats.when(date))
        } else {
            Text("Never").foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private func hooks(_ detail: WatcherDetail) -> some View {
        CardTitle(title: "Hooks listening", systemImage: "bolt") {
            Text(detail.hooks.count == 1 ? "1 hook" : "\(detail.hooks.count) hooks")
                .font(.subheadline)
                .foregroundStyle(.tertiary)
        }
        Divider().padding(.leading, SettingsMetrics.rowInset)
        if detail.hooks.isEmpty {
            Text("No hook listens to it, so it doesn't run.")
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity)
                .padding(16)
        }
        ForEach(Array(detail.hooks.enumerated()), id: \.element) { index, hook in
            if index > 0 { Divider().padding(.leading, 38) }
            let entry = model.status?.hook(hook)
            ListRow(help: "Show \(hook)", open: { openHook(hook) }) {
                HealthDot(health: entry?.health, size: 10)
                Text(hook).font(.body.weight(.medium)).frame(maxWidth: .infinity, alignment: .leading)
                if let summary = entry?.summary {
                    Text(summary)
                        .font(.subheadline)
                        .foregroundStyle(entry?.health == "error" ? Color.red : Color.secondary)
                        .lineLimit(1)
                }
            }
        }
    }

    @ViewBuilder private func access(_ detail: WatcherDetail) -> some View {
        CardTitle(title: "What it runs", systemImage: "lock.shield") {
            if detail.trusted {
                Tag(text: "Trusted", tint: .green)
            } else {
                Tag(text: "Not trusted", tint: .orange)
            }
        }
        Divider().padding(.leading, SettingsMetrics.rowInset)
        ProgramRows(
            program: model.review?.program ?? detail.program,
            signIn: SignInAction(working: model.signingIn, error: model.signInError) { [model] in model.signIn() },
            showsWrites: false)
        if let changes = model.review?.changes, !changes.isEmpty {
            InfoRow(label: "Changed") {
                ForEach(changes, id: \.self) { change in
                    Label(change, systemImage: "arrow.triangle.2.circlepath").font(.subheadline)
                }
            }
        }
        if let error = model.trustError {
            HStack(spacing: 8) {
                RowError(message: error)
                if model.review == nil { Button("Try Again", action: model.loadTrust) }
            }
            .padding(.horizontal, SettingsMetrics.rowInset)
            .padding(.bottom, 8)
        } else if !detail.trusted, model.review == nil {
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text("Reading what it runs…").font(.subheadline).foregroundStyle(.secondary)
            }
            .padding(.horizontal, SettingsMetrics.rowInset)
            .padding(.bottom, 8)
        }
        if !detail.trusted {
            Divider().padding(.leading, SettingsMetrics.rowInset)
            HStack(spacing: 12) {
                Text("Turning on a hook that listens to it trusts it too; this trusts it alone.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 8)
                Button("Trust", action: model.trust)
                    .keyboardShortcut(.defaultAction)
                    .disabled(model.working || model.review == nil)
            }
            .padding(.horizontal, SettingsMetrics.rowInset)
            .padding(.vertical, 12)
        } else {
            Spacer().frame(height: 4)
        }
    }
}

/// A row in a list that opens something: its content, then a chevron; the
/// whole row is the button, and it highlights on hover.
struct ListRow<Content: View>: View {
    let help: String
    let open: () -> Void
    @ViewBuilder var content: Content
    @State private var hovering = false

    var body: some View {
        Button(action: open) {
            HStack(alignment: .center, spacing: 12) {
                content
                Image(systemName: "chevron.right")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundStyle(.tertiary)
            }
            .padding(.horizontal, SettingsMetrics.rowInset)
            .padding(.vertical, 12)
            .background(hovering ? Color.primary.opacity(0.05) : .clear)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(help)
    }
}
