import AutomatorUICore
import SwiftUI

/// Every hook (`hooks list`) with its state from run status, and every
/// watcher (`watchers list`): what the Hooks window shows.
@MainActor
final class HooksModel: ObservableObject {
    @Published var hooks: [HookInfo] = []
    @Published var watchers: [WatcherInfo] = []
    @Published var status: RunStatus?
    @Published var loadError: String?
    @Published var watchersError: String?
    @Published var loaded = false
    let cli: CLI
    private var generation = 0

    init(cli: CLI, status: RunStatus?) {
        self.cli = cli
        self.status = status
    }

    func load() {
        generation += 1
        let mine = generation
        Task {
            async let hooks = cli.background { $0.hooks() }
            async let watchers = cli.background { $0.watchers() }
            let (h, w) = await (hooks, watchers)
            guard mine == generation else { return }
            switch h {
            case .success(let list):
                self.hooks = list
                loadError = nil
            case .failure(let failure):
                loadError = failure.message
            }
            switch w {
            case .success(let list):
                self.watchers = list
                watchersError = nil
            case .failure(let failure):
                watchersError = failure.message
            }
            loaded = true
        }
    }

    /// A new run status: the lists are read again when what the hooks are
    /// changed (a run ending, one turned off, a watcher's state).
    func update(_ status: RunStatus?) {
        let shape = { (s: RunStatus?) in
            s?.hooks?.map { [$0.name, $0.lastRun?.outcome.rawValue, String($0.enabled), $0.problem] }
        }
        let changed = shape(self.status) != shape(status)
        self.status = status
        if changed { load() }
    }
}

struct HooksListView: View {
    @ObservedObject var model: HooksModel
    let openHook: (String) -> Void
    /// Opens a hook's window on its Turn On review.
    let turnOn: (String) -> Void
    let openWatcher: (String) -> Void

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Hooks").font(.title2.weight(.semibold))
                    Text("Prompts and programs run when something happens, instead of on a schedule.")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
                if !RunStatus.responding(model.status) {
                    Banner(
                        title: "Not responding",
                        message: "The scheduler isn't updating its status, so what's shown may be out of date.",
                        systemImage: "exclamationmark.triangle.fill", tint: .red)
                }
                hooks
                Text("Watchers").font(.headline).padding(.top, 6)
                watchers
                Text(
                    "Add a hook or a watcher from a terminal: `\(AppInfo.command) guide hooks` says how."
                )
                .font(.subheadline)
                .foregroundStyle(.secondary)
            }
            .padding(20)
            .frame(maxWidth: 760)
            .frame(maxWidth: .infinity)
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 520, idealWidth: 620, minHeight: 320, idealHeight: 520)
    }

    @ViewBuilder private var hooks: some View {
        if let error = model.loadError {
            Banner(title: "Couldn't read the hooks", message: error, systemImage: "xmark.octagon.fill", tint: .red)
        } else if model.loaded && model.hooks.isEmpty {
            SettingsCard {
                Text("No hooks.").foregroundStyle(.secondary).frame(maxWidth: .infinity).padding(24)
            }
        } else if !model.hooks.isEmpty {
            SettingsCard {
                ForEach(Array(model.hooks.enumerated()), id: \.element.name) { index, hook in
                    if index > 0 { Divider().padding(.leading, 38) }
                    HStack(spacing: 0) {
                        HookListRow(hook: hook, status: model.status?.hook(hook.name)) { openHook(hook.name) }
                        if hook.turnOn == true { TurnOnButton { turnOn(hook.name) } }
                    }
                }
            }
        }
    }

    @ViewBuilder private var watchers: some View {
        if let error = model.watchersError {
            Banner(title: "Couldn't read the watchers", message: error, systemImage: "xmark.octagon.fill", tint: .red)
        } else if model.loaded && model.watchers.isEmpty {
            SettingsCard {
                Text("No watchers.").foregroundStyle(.secondary).frame(maxWidth: .infinity).padding(24)
            }
        } else if !model.watchers.isEmpty {
            SettingsCard {
                ForEach(Array(model.watchers.enumerated()), id: \.element.name) { index, watcher in
                    if index > 0 { Divider().padding(.leading, SettingsMetrics.rowInset) }
                    WatcherListRow(watcher: watcher) { openWatcher(watcher.name) }
                }
            }
        }
    }
}

/// One hook in the list: its health and name, kind, what it acts on in
/// words, whether it's on, its last run; the whole row opens it.
struct HookListRow: View {
    let hook: HookInfo
    let status: RoutineStatus?
    let open: () -> Void

    var body: some View {
        ListRow(help: "Show \(hook.name)", open: open) {
            HealthDot(health: status?.health, size: 10)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(hook.name).font(.title3.weight(.semibold))
                    Tag(text: HookWords.kind(program: hook.isProgram))
                }
                Text(hook.on.text).font(.subheadline).foregroundStyle(.secondary)
                if let last = status?.lastRun ?? hook.lastRun {
                    HStack(spacing: 6) {
                        Text("Last run \(Formats.when(last.startedAt))")
                        OutcomeTag(outcome: last.outcome.rawValue)
                        if let reason = last.reason, !reason.isEmpty {
                            Text(reason).lineLimit(1).truncationMode(.tail)
                        }
                    }
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            if let summary = status?.summary {
                Text(summary)
                    .font(.subheadline)
                    .foregroundStyle(status?.health == "error" ? Color.red : Color.secondary)
                    .lineLimit(1)
            }
        }
    }
}

/// One watcher in the list: its name, how it runs, trusted and paused, the
/// hooks listening, and why it's failing; the whole row opens it.
struct WatcherListRow: View {
    let watcher: WatcherInfo
    let open: () -> Void

    var body: some View {
        ListRow(help: "Show \(watcher.name)", open: open) {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(watcher.name).font(.body.weight(.semibold))
                    WatcherTags(trusted: watcher.trusted, enabled: watcher.enabled, error: watcher.error)
                }
                Text(watcher.modeText).font(.subheadline).foregroundStyle(.secondary)
                Text(watcher.hooks.isEmpty ? "No hook listens" : "Heard by \(watcher.hooks.joined(separator: ", "))")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                if let error = watcher.error, !error.isEmpty {
                    Group {
                        if let at = watcher.errorAt {
                            Text(verbatim: "\(error) (since \(Formats.when(at)))")
                        } else {
                            Text(verbatim: error)
                        }
                    }
                    .font(.subheadline)
                    .foregroundStyle(.red)
                    .lineLimit(2)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}
