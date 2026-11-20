import AppKit
import AutomatorUICore

/// Updates, as the core reports them: `update --check` on launch and daily,
/// and installing with `update --detach`. Whether one is available or
/// installing is the core's answer, never guessed here.
@MainActor
final class Updates: NSObject {
    private let cli: CLI?
    /// What the core last said (`update --check`, or the update it started).
    private var update: Components.Schemas.UpdateOutput?
    private var timer: Timer?
    /// The menu's update item while the menu is open, so a fresh answer shows at once.
    private weak var menuItem: NSMenuItem?

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
        if update?.inProgress == true {
            item.title = "Updating…"
            item.toolTip = "The app restarts when it's done."
            item.action = nil
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
                showAlert("An update is installing", "The app restarts when it's done.")
            case .success(let r): showAlert("You're up to date", "This is \(r.current).")
            }
        }
    }

    private func show(_ answer: Components.Schemas.UpdateOutput) {
        update = answer
        if let menuItem { configure(menuItem) }
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
                if answer.started != true {
                    showAlert("Nothing to update", "This is \(answer.current), the latest release.")
                }
            case .failure(let failure):
                showAlert("The update didn't start", failure.message)
            }
        }
    }
}
