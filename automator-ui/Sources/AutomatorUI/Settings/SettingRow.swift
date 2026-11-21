import AppKit
import AutomatorUICore
import SwiftUI

/// One setting: its label and description on the left, its control (by
/// type) on the right, its error under the description.
struct SettingRow: View {
    let setting: SettingState
    /// The key without its groups' names (the full key shows on hover).
    let label: String
    let error: String?
    let set: (String) -> Void
    let reset: () -> Void
    @State private var draft = ""
    @FocusState private var editing: Bool

    private var notImplemented: Bool { setting.notImplemented == true }
    /// Why it doesn't apply to the backend in use, as the core says ("only
    /// used with llm.backend api"); nil when it does.
    private var inactive: String? { setting.inactive.flatMap { $0.isEmpty ? nil : $0 } }

    var body: some View {
        HStack(alignment: .center, spacing: 16) {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(label).help(setting.key)
                    if notImplemented { NotImplementedTag() }
                    if let inactive { Tag(text: inactive).help("It has no effect with the backend in use.") }
                }
                if !setting.description.isEmpty {
                    Text(setting.description)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                // A choice's own line (e.g. the backend's), for the one selected.
                if let help = setting.enumHelp?.additionalProperties[setting.valueText], !help.isEmpty {
                    Text(help)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if let error { RowError(message: error) }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            // Not applying to the backend in use: dimmed, its tag says why.
            .opacity(inactive == nil ? 1 : 0.55)
            HStack(spacing: 8) {
                if !setting.isDefault {
                    Button("Reset", action: reset)
                        .buttonStyle(.link)
                        .font(.subheadline)
                        .help("Back to the default: \(setting.defaultText.isEmpty ? "unset" : setting.defaultText)")
                }
                control
                if let unit = setting.unit, !unit.isEmpty {
                    Text(unit).foregroundStyle(.secondary)
                }
            }
            .fixedSize()
            .disabled(notImplemented || inactive != nil)
        }
        .padding(.horizontal, SettingsMetrics.rowInset)
        .padding(.vertical, 10)
        .onAppear { draft = setting.valueText }
        .onChange(of: setting.valueText) { _, new in draft = new }
        // Leaving a field saves it, as Return does: an edit never just sits there.
        .onChange(of: editing) { _, now in
            if !now, draft != setting.valueText { set(draft) }
        }
    }

    @ViewBuilder private var control: some View {
        switch setting._type {
        case .bool:
            Toggle(label, isOn: Binding(get: { setting.valueText == "true" }, set: { set($0 ? "true" : "false") }))
                .labelsHidden()
                .toggleStyle(.switch)
                .controlSize(.small)
        case ._enum:
            Picker(label, selection: Binding(get: { setting.valueText }, set: { set($0) })) {
                ForEach(setting._enum ?? [], id: \.self) { Text($0).tag($0) }
            }
            .labelsHidden()
            .fixedSize()
        default:
            TextField(label, text: $draft, prompt: Text(setting.defaultText.isEmpty ? "unset" : setting.defaultText))
                .labelsHidden()
                .textFieldStyle(.roundedBorder)
                .multilineTextAlignment(numeric ? .trailing : .leading)
                .frame(width: fieldWidth)
                .focused($editing)
                .onSubmit { set(draft) }
            if setting._type == .path {
                Button("Choose…") {
                    let panel = NSOpenPanel()
                    panel.canChooseDirectories = true
                    panel.canChooseFiles = true
                    if panel.runModal() == .OK, let url = panel.url { set(url.path) }
                }
            }
        }
    }

    private var numeric: Bool { [.int, .number, .percent].contains(setting._type) }

    /// Numbers and durations share one narrow width so they line up; free
    /// text gets room.
    private var fieldWidth: CGFloat {
        switch setting._type {
        case .int, .number, .percent, .duration: 72
        case .cron: 140
        default: 200
        }
    }
}

/// Marks a setting that can be set but has no effect yet.
struct NotImplementedTag: View {
    var body: some View {
        Text("Not implemented")
            .font(.caption.weight(.medium))
            .foregroundStyle(.orange)
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .background(Color.orange.opacity(0.14), in: Capsule())
            .help("It can be set, but has no effect yet.")
    }
}

/// The core's message when it refused a change.
struct RowError: View {
    let message: String

    var body: some View {
        Label(message, systemImage: "exclamationmark.circle.fill")
            .font(.subheadline)
            .foregroundStyle(.red)
            .fixedSize(horizontal: false, vertical: true)
    }
}
