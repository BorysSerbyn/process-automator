import AutomatorUICore
import SwiftUI

/// The one review before turning something on: what it does in a line,
/// what to weigh, what it still needs before it works (sign-ins), and the
/// details (what it can reach or runs, its watcher's) folded underneath.
/// Setup shows it in its turn-on steps; a window, in its Turn On sheet.
struct TurnOnReview: View {
    let summary: String?
    let review: TrustReview?
    let loadError: String?
    let retry: () -> Void
    var signingIn: String?
    var signInError: String?
    let signIn: (String) -> Void
    @State private var detailsOpen = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            if let summary, !summary.isEmpty {
                Text(summary).fixedSize(horizontal: false, vertical: true)
            }
            if let review {
                reviewed(review)
            } else if let loadError {
                RowError(message: loadError)
                Button("Try Again", action: retry)
            } else {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Reading what it can reach…").font(.subheadline).foregroundStyle(.secondary)
                }
            }
        }
    }

    @ViewBuilder private func reviewed(_ review: TrustReview) -> some View {
        ForEach(review.warnings ?? [], id: \.self) { warning in
            Banner(title: "Before you turn it on", message: warning, systemImage: "exclamationmark.triangle.fill")
        }
        // What it needs before it works. Sign-ins go here; so would anything
        // else it needs from me first.
        if let signIns = review.signIns, !signIns.isEmpty {
            VStack(alignment: .leading, spacing: 6) {
                ForEach(signIns, id: \.self) { name in
                    HStack(spacing: 8) {
                        Image(systemName: "key.fill").foregroundStyle(.orange)
                        Text("\(name) needs signing in").frame(maxWidth: .infinity, alignment: .leading)
                        if signingIn == name {
                            ProgressView().controlSize(.small)
                            Text("In your browser…").font(.subheadline).foregroundStyle(.secondary)
                        } else {
                            Button("Sign In…") { signIn(name) }
                                .disabled(signingIn != nil)
                                .help("Enter what it needs on a page in your browser; it's stored in the Keychain")
                        }
                    }
                }
                if let signInError { RowError(message: signInError) }
                Text("It can be turned on now; it works once signed in.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
        }
        if let watcher = review.watcherToTrust {
            Label(
                "Its watcher, \(watcher.name), is trusted with it: it's what tells \(review.routine) something happened.",
                systemImage: "antenna.radiowaves.left.and.right"
            )
            .font(.subheadline)
            .fixedSize(horizontal: false, vertical: true)
        }
        if let watcher = review.missingWatcher {
            RowError(
                message:
                    "It listens to \(watcher.name): \(watcher.untrusted ?? "no such watcher"). It hears nothing until there is one."
            )
        }
        if let error = review.toolsError, !error.isEmpty {
            RowError(message: "Couldn't check which of its tools write: \(error)")
        }
        DisclosureGroup("Details", isExpanded: $detailsOpen) {
            SettingsCard(nested: true) {
                TrustReviewView(review: review, error: nil, trust: nil, actionTitle: "", note: nil)
                if let watcher = review.watcherToTrust, let program = watcher.program {
                    Divider().padding(.leading, SettingsMetrics.rowInset)
                    CardTitle("Its watcher, \(watcher.name)", systemImage: "antenna.radiowaves.left.and.right")
                    if let description = watcher.description, !description.isEmpty {
                        InfoRow(label: "Watches") { Text(description) }
                    }
                    ProgramRows(program: program, showsWrites: false)
                    if let changes = watcher.changes, !changes.isEmpty {
                        InfoRow(label: "Changed") {
                            ForEach(changes, id: \.self) { Text($0).font(.subheadline) }
                        }
                    }
                }
            }
            .padding(.top, 6)
        }
    }
}

/// The Turn On sheet: the one review, then one confirm (Trust and Turn On,
/// or Turn On when nothing's left to trust). It closes once it's on; why it
/// couldn't stays in it.
struct TurnOnSheet: View {
    @ObservedObject var turnOn: TurnOn
    let summary: String?
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Turn on \(turnOn.name)").font(.title3.weight(.semibold))
            ScrollView {
                TurnOnReview(
                    summary: summary, review: turnOn.review, loadError: turnOn.loadError,
                    retry: { Task { await turnOn.load() } }, signingIn: turnOn.signingIn,
                    signInError: turnOn.signInError, signIn: { name in Task { await turnOn.signIn(name) } }
                )
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: 460)
            if let error = turnOn.error { RowError(message: error) }
            HStack {
                if turnOn.working { ProgressView().controlSize(.small) }
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(turnOn.review?.confirmTitle ?? "Trust and Turn On") { Task { await turnOn.confirm() } }
                    .keyboardShortcut(.defaultAction)
                    .disabled(turnOn.review == nil || turnOn.working)
            }
        }
        .padding(20)
        .frame(width: 540)
    }
}

/// Shows a window's Turn On sheet while its review is showing.
struct TurnOnPresenter: ViewModifier {
    @ObservedObject var turnOn: TurnOn
    let summary: String?

    func body(content: Content) -> some View {
        content.sheet(isPresented: $turnOn.showing) { TurnOnSheet(turnOn: turnOn, summary: summary) }
    }
}
