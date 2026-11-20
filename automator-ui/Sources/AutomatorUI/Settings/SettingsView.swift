import AppKit
import AutomatorUICore
import SwiftUI

typealias SettingState = Components.Schemas.State
typealias LoginItems = Components.Schemas.LoginItemsOutput

/// The app-wide settings, as the core lists them (`settings list --scope
/// app`), and whether the app starts at login (`login-items`). Each setting
/// is shown by its type and sent back as text (`settings set`); the core
/// validates, and its message shows when it refuses a value.
@MainActor
final class SettingsModel: ObservableObject {
    @Published var settings: [SettingState] = []
    @Published var errors: [String: String] = [:]
    @Published var loadError: String?
    @Published var loginItems: LoginItems?
    @Published var loginError: String?
    /// LLM spend against each budget (`usage`).
    @Published var usage: Components.Schemas.Report?
    @Published var usageError: String?
    /// The settings groups open in the window (their KeyTree ids). Top-level
    /// groups start open, subgroups closed; what's open stays open while the
    /// app runs.
    @Published var expanded: Set<String> = []
    let cli: CLI
    /// Loads overlap when changes come quickly; only the latest one counts.
    private var generation = 0
    private var openedDefaults = false

    /// openGroups: the groups open when the window was last closed (nil the
    /// first time: the top-level ones open).
    init(cli: CLI, openGroups: Set<String>? = nil) {
        self.cli = cli
        if let openGroups {
            expanded = openGroups
            openedDefaults = true
        }
    }

    func load() {
        generation += 1
        let mine = generation
        Task {
            async let list = cli.background { $0.appSettings() }
            async let login = cli.background { $0.loginItems() }
            let (settings, items) = await (list, login)
            guard mine == generation else { return }
            switch settings {
            case .success(let list):
                self.settings = list
                loadError = nil
                openDefaults()
            case .failure(let failure):
                loadError = "Couldn't read the settings: \(failure.message)"
            }
            loginItems = try? items.get()
        }
        loadUsage()
    }

    private var usageGeneration = 0

    /// Reads LLM usage on its own: on a Claude plan the core may read it
    /// from Claude Code first (a few seconds), which mustn't hold up the
    /// settings. Only the latest answer counts.
    func loadUsage() {
        usageGeneration += 1
        let mine = usageGeneration
        Task {
            let result = await cli.background { $0.usage() }
            guard mine == usageGeneration else { return }
            switch result {
            case .success(let report):
                usage = report
                usageError = nil
            case .failure(let failure):
                usageError = failure.message
            }
        }
    }

    /// Opens the top-level groups the first time there are settings, unless
    /// something was opened already.
    private func openDefaults() {
        guard !openedDefaults, !settings.isEmpty else { return }
        openedDefaults = true
        if expanded.isEmpty { expanded = Set(KeyTree.build(settings) { $0.key }.map(\.id)) }
    }

    func isOpen(_ id: String) -> Bool { expanded.contains(id) }

    func toggle(_ id: String) {
        withAnimation(.easeInOut(duration: 0.18)) {
            if expanded.remove(id) == nil { expanded.insert(id) }
        }
    }

    func set(_ key: String, _ text: String) {
        apply(key) { $0.setSetting(key, text) }
    }

    func reset(_ key: String) {
        apply(key) { $0.resetSetting(key) }
    }

    private func apply(_ key: String, _ change: @escaping @Sendable (CLI) -> Result<SettingState, CLI.Failure>) {
        Task {
            let result = await cli.background(change)
            if case .failure(let failure) = result {
                errors[key] = failure.message
            } else {
                errors[key] = nil
            }
            load()
        }
    }

    func setStartAtLogin(_ on: Bool) {
        Task {
            switch await cli.background({ $0.setStartAtLogin(on) }) {
            case .success(let items):
                loginItems = items
                loginError = nil
            case .failure(let failure):
                loginError = failure.message
            }
        }
    }
}

/// The settings window: Start at login, then one card per top-level group,
/// each a collapsible section with its subgroups nested inside.
struct SettingsView: View {
    @ObservedObject var model: SettingsModel

    var body: some View {
        Group {
            if let error = model.loadError {
                ContentUnavailableView {
                    Label("No settings", systemImage: "exclamationmark.triangle")
                } description: {
                    Text(error)
                }
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 14) {
                        SettingsCard { startAtLogin }
                        SettingsCard { UsageView(report: model.usage, error: model.usageError) }
                            // While it shows, read usage again every 15 minutes (the core
                            // reads Claude Code's again once its reading is 10 minutes old).
                            .task {
                                while !Task.isCancelled {
                                    try? await Task.sleep(for: .seconds(15 * 60))
                                    if !Task.isCancelled { model.loadUsage() }
                                }
                            }
                        ForEach(KeyTree.build(model.settings) { $0.key }) { group in
                            SettingsGroupView(group: group, model: model)
                        }
                    }
                    .padding(20)
                    .frame(maxWidth: 760)
                    .frame(maxWidth: .infinity)
                }
            }
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 580, idealWidth: 640, minHeight: 420, idealHeight: 680)
    }

    private var startAtLogin: some View {
        let installed = model.loginItems?.installed == true
        return HStack(alignment: .center, spacing: 16) {
            VStack(alignment: .leading, spacing: 3) {
                Text("Start at login")
                Text(
                    installed
                        ? "Process Automator starts when you log in."
                        : "Available once the app is installed."
                )
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
                if let error = model.loginError { RowError(message: error) }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Toggle(
                "Start at login",
                isOn: Binding(
                    get: { model.loginItems?.enabled == true },
                    set: { model.setStartAtLogin($0) }
                )
            )
            .labelsHidden()
            .toggleStyle(.switch)
            .controlSize(.small)
            .disabled(!installed)
        }
        .padding(.horizontal, SettingsMetrics.rowInset)
        .padding(.vertical, 12)
    }
}
