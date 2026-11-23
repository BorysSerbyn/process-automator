import AutomatorUICore
import SwiftUI

/// The Acknowledgements window: each notification a routine or hook asked
/// me to acknowledge, newest at the bottom (what sent it, its title and
/// text, when, and its run), with Acknowledge on each and Acknowledge All.
/// It's read again when run status says what waits changed.
struct AcknowledgementsView: View {
    @ObservedObject var model: Acknowledgements
    /// Opens what sent one on its run: a routine's or hook's window.
    let openRun: (_ name: String, _ run: String?, _ hook: Bool) -> Void

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                header
                if let error = model.error { RowError(message: error) }
                if let error = model.loadError {
                    Banner(
                        title: "Couldn't read what's waiting", message: error, systemImage: "xmark.octagon.fill",
                        tint: .red)
                } else if model.loaded && model.waiting.isEmpty {
                    SettingsCard {
                        VStack(spacing: 6) {
                            Image(systemName: "checkmark.circle").font(.title).foregroundStyle(.green)
                            Text("Nothing waits for you.").foregroundStyle(.secondary)
                        }
                        .frame(maxWidth: .infinity)
                        .padding(24)
                    }
                } else if !model.waiting.isEmpty {
                    SettingsCard {
                        ForEach(Array(model.waiting.enumerated()), id: \.element.id) { index, note in
                            if index > 0 { Divider().padding(.leading, SettingsMetrics.rowInset) }
                            AcknowledgementRow(
                                note: note, working: model.acknowledging.contains(note.id),
                                acknowledge: { Task { await model.acknowledge(note.id) } },
                                openRun: note.routine.map { name in { openRun(name, note.run, note.hook == true) } })
                        }
                    }
                } else {
                    ProgressView().controlSize(.small).frame(maxWidth: .infinity)
                }
            }
            .padding(20)
            .frame(maxWidth: 680)
            .frame(maxWidth: .infinity)
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 460, idealWidth: 560, minHeight: 280, idealHeight: 440)
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Acknowledgements").font(.title2.weight(.semibold))
                Text("Notifications that wait for you to acknowledge them; the dot stays red until you do.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer()
            if model.waiting.count > 1 {
                Button("Acknowledge All") { Task { await model.acknowledgeAll() } }
                    .buttonStyle(.borderedProminent)
                    .disabled(model.acknowledging.contains("*"))
            }
        }
    }
}

/// One notification waiting: what sent it and when, its title and text
/// (the routine's words, shown as plain text), Show Run, and Acknowledge.
struct AcknowledgementRow: View {
    let note: CoreNotification
    let working: Bool
    let acknowledge: () -> Void
    let openRun: (() -> Void)?

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: note.urgent == true ? "exclamationmark.circle.fill" : "bell.fill")
                .foregroundStyle(note.urgent == true ? Color.red : Color.orange)
                .padding(.top, 2)
            VStack(alignment: .leading, spacing: 4) {
                Text(verbatim: note.title).font(.body.weight(.semibold)).fixedSize(horizontal: false, vertical: true)
                if let body = note.body, !body.isEmpty {
                    Text(verbatim: body).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                }
                HStack(spacing: 6) {
                    if let routine = note.routine { Text(verbatim: routine).font(.subheadline.monospaced()) }
                    Text(Formats.when(note.at))
                    if let openRun {
                        Button("Show Run", action: openRun).buttonStyle(.link)
                    }
                }
                .font(.subheadline)
                .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            if working {
                ProgressView().controlSize(.small)
            } else {
                Button("Acknowledge", action: acknowledge)
            }
        }
        .padding(.horizontal, SettingsMetrics.rowInset)
        .padding(.vertical, 12)
    }
}
