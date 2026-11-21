import AppKit
import AutomatorUICore
import SwiftUI

/// Updates, as the core reports them: `update --check` on launch and daily,
/// installing with `update --detach`, and while one installs, what it's
/// doing (`update progress`, every 2 seconds), in the menu and the Updating
/// window. Whether one is available or installing, and what it's doing, is
/// the core's answer, never guessed here.
@MainActor
final class Updates: NSObject, NSWindowDelegate {
    private let cli: CLI?
    /// What the core last said (`update --check`, or the update it started).
    private var update: Components.Schemas.UpdateOutput?
    private var timer: Timer?
    /// The menu's update item while the menu is open, so a fresh answer shows at once.
    private weak var menuItem: NSMenuItem?
    /// The update installing (or the one that last ended), while it's followed.
    private let progress = UpdateProgressModel()
    private var following: Timer?
    private var asking = false
    private var window: NSWindow?

    init(cli: CLI?) { self.cli = cli }

    func start() {
        check(quietly: true)
        timer = Timer.scheduledTimer(withTimeInterval: 24 * 60 * 60, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.check(quietly: true) }
        }
    }

    /// The menu's update item: installing, "Update to X…", or "Check for Updates".
    func makeMenuItem() -> NSMenuItem {
        let item = NSMenuItem()
        configure(item)
        menuItem = item
        // While an update installs, ask again: it may have failed (it never
        // restarts the app then).
        if update?.inProgress == true { check(quietly: true) }
        return item
    }

    private func configure(_ item: NSMenuItem) {
        item.target = self
        item.keyEquivalent = ""
        item.toolTip = nil
        if update?.inProgress == true || progress.installing {
            let tag = progress.current?.tag ?? update?.latest ?? ""
            item.title = tag.isEmpty ? "Updating…" : "Updating to \(tag)…"
            item.toolTip = progress.current?.step ?? "The app restarts when it's done."
            item.action = #selector(showProgress)
        } else if let update, update.updateAvailable {
            item.title = "Update to \(update.latest)…"
            item.action = #selector(install)
        } else {
            item.title = "Check for Updates"
            item.action = #selector(checkNow)
        }
    }

    @objc private func checkNow() { check(quietly: false) }

    /// Asks the core whether a newer release exists; quietly, only the menu changes.
    private func check(quietly: Bool) {
        guard let cli else { return }
        Task {
            let result = await cli.background { $0.updateCheck() }
            if case .success(let answer) = result { show(answer) }
            guard !quietly else { return }
            switch result {
            case .failure(let failure): showAlert("Couldn't check for updates", failure.message)
            case .success(let r) where r.updateAvailable && r.inProgress != true: install()
            case .success(let r) where r.inProgress == true:
                showProgress()
            case .success(let r): showAlert("You're up to date", "This is \(r.current).")
            }
        }
    }

    private func show(_ answer: Components.Schemas.UpdateOutput) {
        update = answer
        if answer.inProgress { follow() }
        if let menuItem { configure(menuItem) }
    }

    // MARK: Following an update

    /// Asks the core what the update is doing every 2 seconds, until it says
    /// it's over.
    private func follow() {
        guard following == nil else { return }
        following = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.askProgress() }
        }
        askProgress()
    }

    private func askProgress() {
        guard let cli, !asking else { return }
        asking = true
        Task {
            let result = await cli.background { $0.updateProgress() }
            asking = false
            guard case .success(let answer) = result else { return }
            progress.current = answer
            if !progress.installing {
                following?.invalidate()
                following = nil
                check(quietly: true)  // the menu: up to date, or the update again
            }
            if let menuItem { configure(menuItem) }
        }
    }

    /// The Updating window: what the update is doing, or how it ended.
    @objc private func showProgress() {
        if window == nil {
            let view = UpdateProgressView(model: progress) { [weak self] in self?.window?.close() }
            let controller = NSHostingController(rootView: view)
            controller.sizingOptions = .preferredContentSize
            let window = NSWindow(contentViewController: controller)
            window.styleMask = [.titled, .closable]
            window.title = "Updating \(AppInfo.name)"
            window.isReleasedWhenClosed = false
            window.center()
            window.delegate = self
            self.window = window
        }
        bringToFront(window)
    }

    func windowWillClose(_ notification: Notification) {
        DispatchQueue.main.async {
            guard self.window?.isVisible == false else { return }  // reopened meanwhile
            self.window = nil
        }
    }

    @objc private func install() {
        guard let cli, let update, update.updateAvailable else { return }
        NSApp.activate()
        let confirm = NSAlert()
        confirm.messageText = "Update to \(update.latest)?"
        confirm.informativeText = update.note ?? ""
        confirm.addButton(withTitle: "Update")
        confirm.addButton(withTitle: "Later")
        guard confirm.runModal() == .alertFirstButtonReturn else { return }
        Task {
            switch await cli.background({ $0.startUpdate() }) {
            case .success(let answer):
                show(answer)
                if answer.started == true {
                    showProgress()
                } else {
                    showAlert("Nothing to update", "This is \(answer.current), the latest release.")
                }
            case .failure(let failure):
                showAlert("The update didn't start", failure.message)
            }
        }
    }
}

/// The update being followed, as the core last described it (`update progress`).
@MainActor
final class UpdateProgressModel: ObservableObject {
    @Published var current: Components.Schemas.UpdateProgress?

    var installing: Bool { current?.state == .installing }
}

/// The Updating window's content: the release, what the update is doing and
/// the latest line it printed, in the core's words; or how it ended.
struct UpdateProgressView: View {
    @ObservedObject var model: UpdateProgressModel
    let close: () -> Void

    private var state: Components.Schemas.UpdateProgress.statePayload? { model.current?.state }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .top, spacing: 12) {
                Image(systemName: icon)
                    .font(.system(size: 28))
                    .foregroundStyle(state == .failed ? Color.red : Color.accentColor)
                VStack(alignment: .leading, spacing: 4) {
                    Text(title).font(.headline)
                    if let started = model.current?.startedAt {
                        Text("Started \(Formats.when(started))").font(.subheadline).foregroundStyle(.secondary)
                    }
                }
            }
            switch state {
            case .failed:
                Text(model.current?.error ?? "").textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                if let log = model.current?.log {
                    Text("Log: \(log)").font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
                }
            case .done:
                Text(model.current?.step ?? "").fixedSize(horizontal: false, vertical: true)
            default:
                ProgressView().progressViewStyle(.linear)
                Text(model.current?.step ?? "Starting…").fixedSize(horizontal: false, vertical: true)
                if let detail = model.current?.detail, !detail.isEmpty {
                    Text(detail)
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                Text("You can close this window: the update carries on, and the app restarts when it's done.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Spacer()
                Button(state == .installing || state == nil ? "Hide" : "Close", action: close)
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 440)
    }

    private var icon: String {
        switch state {
        case .failed: "xmark.octagon.fill"
        case .done: "checkmark.circle.fill"
        default: "arrow.down.circle"
        }
    }

    private var title: String {
        let tag = model.current?.tag.map { " to \($0)" } ?? ""
        switch state {
        case .failed: return "The update\(tag) failed"
        case .done: return "Update finished"
        default: return "Updating\(tag)"
        }
    }
}
