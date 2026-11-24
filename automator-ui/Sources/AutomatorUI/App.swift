import AppKit
import AutomatorUICore
import SwiftUI

/// automator-ui: the indicator (menu bar icon and menu), the app's window
/// (its screens in a sidebar), the app's own desktop notifications and updates. It
/// shows what the core says and runs what the core offers, through its CLI
/// only: it reads none of the core's files.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private let cli = CLI.bundled
    private var indicator: Indicator!
    private var updates: Updates!
    private let notifier = Notifier()

    /// The core's run status, followed through `status --watch`; the last
    /// one it printed (nil: none yet, or unreadable).
    private var statusStream: StatusStream?
    private var status: RunStatus?
    private var refreshTimer: Timer?
    private var ticks = 0

    /// The app's window; nil without the CLI.
    private var window: MainWindow?

    func applicationDidFinishLaunching(_ notification: Notification) {
        // One copy at a time: the login item and a copy macOS opened by
        // itself (after a permission is granted, "Quit & Reopen") would each
        // put an indicator in the menu bar. The newer one leaves, quietly
        // (a successful exit: launchd doesn't start it again).
        if anotherCopyIsRunning() {
            NSApp.terminate(nil)
            return
        }
        updates = Updates(cli: cli)
        if let cli {
            // After a setup action, the app's notifications are asked for
            // again; run status follows on its own (status --watch).
            window = MainWindow(cli: cli, updates: updates, changed: { [weak self] in self?.reload() })
        }
        updates.open = { [weak self] in self?.window?.show(.updates) }
        // The menu's items, each opening its screen in the app's window.
        indicator = Indicator(
            updates: updates, quit: { [weak self] in self?.quit() },
            openSettings: { [weak self] in self?.window?.show(.settings) },
            openRoutines: { [weak self] in self?.window?.show(.routines) },
            openRoutine: { [weak self] name in self?.window?.show(.routine(name), review: true) },
            openRun: { [weak self] id in self?.openRun(id) },
            openHooks: { [weak self] in self?.window?.show(.hooks) },
            openHook: { [weak self] name, run in self?.window?.show(.hook(name), run: run, review: run == nil) },
            openAcknowledgements: { [weak self] in self?.window?.show(.acknowledgements) },
            openWatcher: { [weak self] name in self?.window?.show(.watcher(name)) },
            openSetup: { [weak self] in self?.window?.show(.setup) })
        notifier.setUp(cli: cli) { [weak self] click in self?.show(click) }
        followStatus()
        updates.start()
        startScheduler()
    }

    // MARK: Opening and quitting

    /// Whether another process runs this same app (this executable), e.g.
    /// one launchd started at login beside one macOS reopened. The core's
    /// own processes in the bundle (the daemon, the CLI) aren't apps.
    private func anotherCopyIsRunning() -> Bool {
        guard let id = Bundle.main.bundleIdentifier, let me = Bundle.main.executableURL?.resolvingSymlinksInPath()
        else {
            return false
        }
        let pid = ProcessInfo.processInfo.processIdentifier
        return NSRunningApplication.runningApplications(withBundleIdentifier: id).contains {
            $0.processIdentifier != pid && $0.executableURL?.resolvingSymlinksInPath() == me
        }
    }

    /// Opening the app starts the scheduler if it isn't running, so opening
    /// it after a Quit brings everything back.
    private func startScheduler() {
        guard let cli else { return }
        Task {
            if let failure = await Lifecycle.opened(cli) {
                NSLog("Couldn't start the scheduler: %@", failure.message)
            }
        }
    }

    /// Opened again while running (from Finder, Spotlight or Launchpad): the
    /// scheduler started, and Setup while setup isn't done, else Settings.
    /// A click on one of its notifications reaches the app as a reopen too,
    /// sent by the notification daemon after the click itself: that one
    /// opens nothing, the click shows what it's about.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        startScheduler()
        if reopenedByNotificationClick() { return false }
        switch Lifecycle.reopenWindow(for: status) {
        case .setup: window?.show(.setup)
        case .settings: window?.show(.settings)
        }
        return false
    }

    /// Quit: the scheduler stops too (both start again at the next login).
    /// The menu bar icon goes at once; when stopping fails, it says why, and
    /// quits anyway.
    private func quit() {
        indicator.hide()
        Task {
            await Lifecycle.quit(
                cli, failed: { showAlert("Couldn't stop the scheduler", $0.message) },
                terminate: { NSApp.terminate(nil) })
        }
    }

    /// Whether the reopen being handled was sent by macOS's notification
    /// daemon (`usernoted`), which sends one for every click on the app's
    /// notifications.
    private func reopenedByNotificationClick() -> Bool {
        guard let event = NSAppleEventManager.shared().currentAppleEvent,
            let pid = event.attributeDescriptor(forKeyword: AEKeyword(keySenderPIDAttr))?.int32Value, pid > 0
        else { return false }
        var path = [CChar](repeating: 0, count: 4096)
        guard proc_pidpath(pid, &path, UInt32(path.count)) > 0 else { return false }
        return URL(fileURLWithPath: String(cString: path)).lastPathComponent == "usernoted"
    }

    /// A click on one of the app's notifications: its link, opened by the
    /// system's default handler; the run it's about, in its routine's (or
    /// hook's) screen; an update's, Updates (what's new); or the app
    /// (Routines), in the app's window.
    private func show(_ click: Components.Schemas.ClickOutput) {
        if click.action == .open_hyphen_url, let link = click.url, let url = URL(string: link) {
            NSWorkspace.shared.open(url)
        } else if click.action == .show_hyphen_run, let routine = click.routine {
            window?.show(click.hook == true ? .hook(routine) : .routine(routine), run: click.run)
        } else if click.action == .show_hyphen_updates {
            window?.show(.updates)
        } else {
            window?.show(.routines)
        }
    }

    private func openRun(_ id: String) {
        guard let cli else { return }
        Task {
            if let failure = await cli.background({ $0.openRun(id) }) {
                showAlert("Couldn't open the run", failure.message)
            }
        }
    }

    // MARK: The core's status

    /// Shows the last status (the indicator, the app's window) and
    /// delivers new notifications.
    private func reload() {
        indicator.show(status)
        window?.update(status)
        notifier.deliver()
    }

    /// Follows run status through the CLI: each status it prints is shown at
    /// once, and the app's notifications asked for (a run's end changes it).
    /// A 15s timer shows a status past its validUntil as red (the core
    /// stopped heartbeating, or the stream did), and asks for notifications
    /// every minute (an update's result changes no status).
    private func followStatus() {
        refreshTimer = Timer.scheduledTimer(withTimeInterval: 15, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated {
                guard let self else { return }
                self.ticks += 1
                self.indicator.show(self.status)
                self.window?.update(self.status)
                if self.ticks % 4 == 0 { self.notifier.deliver() }
            }
        }
        guard let cli else {
            indicator.show(nil)
            return
        }
        let stream = StatusStream(cli: cli) { [weak self] status in
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self else { return }
                    self.status = status
                    self.reload()
                }
            }
        }
        statusStream = stream
        stream.start()
        reload()
    }
}

/// An alert in front of everything (the app's window may not be open).
@MainActor
func showAlert(_ title: String, _ text: String) {
    NSApp.activate()
    let alert = NSAlert()
    alert.messageText = title
    alert.informativeText = text
    alert.runModal()
}
