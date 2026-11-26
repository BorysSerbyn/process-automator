import AutomatorUICore
import SwiftUI

/// The Template Updates screen: the installs whose template changed in this
/// version, a checkbox each (none ticked), and Select All, Not Now and
/// Override under them; once chosen, what was overridden.
struct TemplateUpdatesView: View {
    @ObservedObject var model: TemplateUpdates
    /// Closes the screen (back to Updates).
    let close: () -> Void

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header
                if let error = model.error {
                    Banner(
                        title: model.loaded && model.updates.isEmpty
                            ? "Couldn't read the template updates" : "Couldn't finish",
                        message: error, systemImage: "xmark.octagon.fill", tint: .red)
                }
                if model.phase == .done {
                    TemplateUpdatesDone(overridden: model.overridden, close: close)
                } else if !model.loaded {
                    ProgressView().controlSize(.small).frame(maxWidth: .infinity).padding(24)
                } else if model.updates.isEmpty {
                    if model.error == nil {
                        Text("Nothing to update.").foregroundStyle(.secondary)
                    }
                } else {
                    SettingsCard {
                        ForEach(Array(model.updates.enumerated()), id: \.element.name) { i, update in
                            if i > 0 { Divider().padding(.leading, SettingsMetrics.rowInset) }
                            TemplateUpdateRow(
                                update: update,
                                ticked: Binding(
                                    get: { model.isTicked(update.name) },
                                    set: { model.setTicked(update.name, $0) }),
                                disabled: model.busy)
                        }
                    }
                    buttons
                }
            }
            .padding(24)
            .frame(maxWidth: 720)
            .frame(maxWidth: .infinity)
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 480, idealWidth: 600, minHeight: 320, idealHeight: 560)
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 3) {
            Text("Template Updates").font(.title2.weight(.semibold))
            Text(
                "Templates you installed from have changed in this version. Tick the ones to replace with the new version; the others keep yours and aren't offered again."
            )
            .font(.subheadline)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
        }
    }

    private var buttons: some View {
        HStack(spacing: 10) {
            Button("Select All") { model.selectAll() }
                .disabled(model.busy || model.selectable.isEmpty)
                .help("Tick every one that can be overridden")
            Spacer(minLength: 12)
            if model.phase == .overriding {
                HStack(spacing: 6) {
                    ProgressView().controlSize(.small)
                    Text("Overriding… this can take a few minutes").foregroundStyle(.secondary)
                }
            }
            Button {
                Task { if await model.notNow() { close() } }
            } label: {
                if model.phase == .skipping {
                    ProgressView().controlSize(.small).frame(minWidth: 64)
                } else {
                    Text("Not Now").frame(minWidth: 64)
                }
            }
            .disabled(model.busy)
            .help("Keep yours; these changes aren't offered again")
            Button {
                Task { await model.override() }
            } label: {
                Text("Override").frame(minWidth: 72)
            }
            .buttonStyle(.borderedProminent)
            .disabled(!model.canOverride)
            .help("Replace the ticked ones with their template's new version")
        }
    }
}

/// One install whose template changed: its checkbox, name, kind and
/// versions, what changed, and what overriding it means.
struct TemplateUpdateRow: View {
    let update: TemplateUpdate
    @Binding var ticked: Bool
    let disabled: Bool

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Toggle("", isOn: $ticked)
                .toggleStyle(.checkbox)
                .labelsHidden()
                .disabled(disabled || update.blockedReason != nil)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(verbatim: update.name).font(.body.weight(.medium))
                    Tag(text: update.kind.rawValue)
                    if let versions = update.versions {
                        Text(verbatim: versions).font(.subheadline.monospacedDigit()).foregroundStyle(.secondary)
                    }
                }
                if let changes = update.changes, !changes.isEmpty {
                    Text(verbatim: changes)
                        .font(.subheadline)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if let with = update.with, !with.isEmpty {
                    note(
                        "Also updates its watcher\(with.count == 1 ? "" : "s") \(with.joined(separator: ", ")).",
                        systemImage: "arrow.triangle.branch", tint: .secondary)
                }
                if let blocked = update.blockedReason {
                    note("Can't be overridden: \(blocked)", systemImage: "nosign", tint: .secondary)
                } else if update.edited == .yes {
                    note(
                        "You've edited it: overriding replaces your edits.",
                        systemImage: "exclamationmark.triangle.fill", tint: .orange)
                } else if update.edited == .unknown {
                    note(
                        "Installed before versions were recorded, so it may have been edited: overriding replaces any edits.",
                        systemImage: "info.circle", tint: .secondary)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.horizontal, SettingsMetrics.rowInset)
        .padding(.vertical, 10)
    }

    private func note(_ text: String, systemImage: String, tint: Color) -> some View {
        Label {
            Text(verbatim: text).fixedSize(horizontal: false, vertical: true)
        } icon: {
            Image(systemName: systemImage).foregroundStyle(tint)
        }
        .font(.subheadline)
        .foregroundStyle(tint == .secondary ? Color.secondary : Color.primary)
    }
}

/// Chosen: what was overridden, and whether each is still trusted (one
/// that isn't needs turning on again in its screen); or that none was.
struct TemplateUpdatesDone: View {
    let overridden: [Components.Schemas.Overridden]
    let close: () -> Void

    var body: some View {
        SettingsCard {
            VStack(alignment: .leading, spacing: 10) {
                Label(
                    overridden.isEmpty ? "Kept yours" : "Updated",
                    systemImage: "checkmark.circle.fill"
                )
                .font(.headline)
                .foregroundStyle(.green)
                if overridden.isEmpty {
                    Text("These template changes won't be offered again.")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
                ForEach(overridden, id: \.name) { item in
                    HStack(spacing: 6) {
                        Text(verbatim: item.name).font(.body.weight(.medium))
                        Tag(text: item.kind.rawValue)
                        if let version = item.version, !version.isEmpty {
                            Text(verbatim: version).font(.subheadline.monospacedDigit()).foregroundStyle(.secondary)
                        }
                        Spacer(minLength: 8)
                        if item.trusted {
                            Tag(text: "Trusted", tint: .green)
                        } else {
                            Tag(text: "Not trusted", tint: .orange)
                        }
                    }
                }
                if overridden.contains(where: { !$0.trusted }) {
                    Text("One that isn't trusted waits for you to review and turn it on in its screen.")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                HStack {
                    Spacer()
                    Button("Done", action: close).buttonStyle(.borderedProminent)
                }
            }
            .padding(16)
        }
    }
}
