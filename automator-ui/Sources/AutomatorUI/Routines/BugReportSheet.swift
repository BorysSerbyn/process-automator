import AutomatorUICore
import SwiftUI

/// Report a Bug…: a title, what happened, and whether to attach the logs;
/// Open on GitHub has the core open the issue, filled in, to submit from my
/// own account. With logs, it then says to drag the zip onto the issue.
struct BugReportSheet: View {
    @ObservedObject var report: BugReport
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Report a bug in \(report.name)").font(.title3.weight(.semibold))
            if report.logsToDrag != nil || report.unopened != nil {
                opened
            } else {
                form
            }
        }
        .padding(20)
        .frame(width: 520)
    }

    @ViewBuilder private var form: some View {
        TextField("Title", text: $report.title, prompt: Text("What went wrong, in a line"))
        VStack(alignment: .leading, spacing: 4) {
            Text("What happened").font(.subheadline.weight(.semibold)).foregroundStyle(.secondary)
            TextEditor(text: $report.description)
                .frame(minHeight: 130)
                .padding(4)
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(Color.primary.opacity(0.15)))
        }
        Toggle("Attach logs", isOn: $report.includeLogs)
            .help("Export its latest runs' logs to drag onto the issue")
        if report.includeLogs {
            Label(BugReport.logsNote, systemImage: "exclamationmark.triangle.fill")
                .font(.subheadline)
                .foregroundStyle(.orange)
                .fixedSize(horizontal: false, vertical: true)
        }
        if let whereText = report.whereText {
            Text(whereText)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        } else if let loadError = report.loadError {
            RowError(message: loadError)
        }
        if let error = report.error { RowError(message: error) }
        HStack {
            if report.working { ProgressView().controlSize(.small) }
            Spacer()
            Button("Cancel") { dismiss() }
                .keyboardShortcut(.cancelAction)
            Button("Open on GitHub") { Task { await report.open() } }
                .keyboardShortcut(.defaultAction)
                .disabled(report.repo == nil || report.working)
                .help("Open the issue in your browser, filled in, to read and submit")
        }
    }

    @ViewBuilder private var opened: some View {
        if let url = report.unopened {
            Text("The browser didn't open. Open the issue from here:")
                .fixedSize(horizontal: false, vertical: true)
            Link("Open the Issue on GitHub", destination: url)
        } else {
            Label("The issue is open in your browser.", systemImage: "checkmark.circle.fill")
        }
        if let dragText = report.dragText {
            Text(dragText).fixedSize(horizontal: false, vertical: true)
        }
        HStack {
            Spacer()
            Button("Done") { report.finish() }
                .keyboardShortcut(.defaultAction)
        }
    }
}

/// Shows a window's Report a Bug… sheet while it's showing.
struct BugReportPresenter: ViewModifier {
    @ObservedObject var report: BugReport

    func body(content: Content) -> some View {
        content.sheet(isPresented: $report.showing) { BugReportSheet(report: report) }
    }
}

/// Export Logs going, or why it failed, under a window's header.
struct ExportLogsStatus: View {
    @ObservedObject var report: BugReport

    var body: some View {
        if report.exporting {
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text("Exporting the logs…").font(.subheadline).foregroundStyle(.secondary)
            }
        } else if let error = report.exportError {
            RowError(message: "Couldn't export the logs: \(error)")
        }
    }
}
