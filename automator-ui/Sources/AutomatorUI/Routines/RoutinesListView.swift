import AutomatorUICore
import SwiftUI

/// Every routine (`routines list`) with its state from run status: what the
/// Routines window shows.
@MainActor
final class RoutinesModel: ObservableObject {
    @Published var routines: [RoutineInfo] = []
    @Published var status: RunStatus?
    @Published var loadError: String?
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
            let result = await cli.background { $0.routines() }
            guard mine == generation else { return }
            switch result {
            case .success(let list):
                routines = list
                loadError = nil
            case .failure(let failure):
                loadError = failure.message
            }
            loaded = true
        }
    }

    /// A new run status. The list itself changes only with what the routines
    /// are (a run finishing, a routine paused or deleted), so it's re-read then.
    func update(_ status: RunStatus?) {
        let before = self.status?.routines.map { [$0.name, $0.lastRun?.outcome.rawValue, String($0.enabled)] }
        let after = status?.routines.map { [$0.name, $0.lastRun?.outcome.rawValue, String($0.enabled)] }
        self.status = status
        if before != after { load() }
    }
}

struct RoutinesListView: View {
    @ObservedObject var model: RoutinesModel
    let openRoutine: (String) -> Void
    let openSettings: () -> Void
    let openSetup: () -> Void

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                header
                if !RunStatus.responding(model.status) {
                    Banner(
                        title: "Not responding",
                        message: "The scheduler isn't updating its status, so what's shown may be out of date.",
                        systemImage: "exclamationmark.triangle.fill", tint: .red)
                }
                if let error = model.loadError {
                    Banner(
                        title: "Couldn't read the routines", message: error, systemImage: "xmark.octagon.fill",
                        tint: .red)
                } else if model.loaded && model.routines.isEmpty {
                    SettingsCard {
                        Text("No routines installed.")
                            .foregroundStyle(.secondary)
                            .frame(maxWidth: .infinity)
                            .padding(24)
                    }
                } else if !model.routines.isEmpty {
                    SettingsCard {
                        ForEach(Array(model.routines.enumerated()), id: \.element.name) { index, routine in
                            if index > 0 { Divider().padding(.leading, 38) }
                            RoutineListRow(routine: routine, status: model.status?.routine(routine.name)) {
                                openRoutine(routine.name)
                            }
                        }
                    }
                }
            }
            .padding(20)
            .frame(maxWidth: 760)
            .frame(maxWidth: .infinity)
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 520, idealWidth: 600, minHeight: 320, idealHeight: 460)
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Routines").font(.title2.weight(.semibold))
                if let summary = model.status?.summary, RunStatus.responding(model.status) {
                    Text(summary).font(.subheadline).foregroundStyle(.secondary)
                }
            }
            Spacer()
            Button(action: openSetup) {
                Label("Setup", systemImage: "checklist")
            }
            .help("Add routines from templates, choose the model backend, sign in, trust and enable routines")
            Button(action: openSettings) {
                Label("Settings", systemImage: "gearshape")
            }
            .help("Open the app's settings")
        }
    }
}

/// One routine in the list: its health and name, its schedule, its last run;
/// the whole row opens it.
struct RoutineListRow: View {
    let routine: RoutineInfo
    let status: RoutineStatus?
    let open: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: open) {
            HStack(alignment: .center, spacing: 12) {
                HealthDot(health: status?.health, size: 10)
                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 6) {
                        Text(routine.name).font(.title3.weight(.semibold))
                        if !routine.trusted { Tag(text: "Not trusted", tint: .orange) }
                    }
                    Text(routine.scheduleText ?? "No schedule")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                    if let last = status?.lastRun ?? routine.lastRun {
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
        .help("Show \(routine.name)")
    }
}
