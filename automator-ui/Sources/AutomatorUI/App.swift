import AppKit
import AutomatorUICore
import SwiftUI

/// automator-ui: the indicator (menu bar icon and menu), the routines and
/// settings windows, the app's own desktop notifications and updates. It
/// shows what the core says and runs what the core offers, through its CLI
/// only: it reads none of the core's files.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
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

    private var routineWindows: RoutineWindows?
    private var setupWindow: SetupWindow?
    private var settingsWindow: NSWindow?
    private var settingsModel: SettingsModel?
    /// The settings groups open when the window last closed: the window and
    /// its data are freed on close (SwiftUI holds a lot), this outlives them.
    private var settingsOpenGroups: Set<String>?

    func applicationDidFinishLaunching(_ notification: Notification) {
        updates = Updates(cli: cli)
        if let cli {
            // After a setup action, the app's notifications are asked for
            // again; run status follows on its own (status --watch).
            setupWindow = SetupWindow(
                cli: cli, openRoutine: { [weak self] name in self?.routineWindows?.showRoutine(name) },
                changed: { [weak self] in self?.reload() })
            routineWindows = RoutineWindows(
                cli: cli, openSettings: { [weak self] in self?.openSettings() },
                openSetup: { [weak self] in self?.setupWindow?.show() })
        }
        indicator = Indicator(
            updates: updates, quit: { [weak self] in self?.quit() },
            openSettings: { [weak self] in self?.openSettings() },
            openRoutines: { [weak self] in self?.routineWindows?.showList() },
            openRoutine: { [weak self] name in self?.routineWindows?.showRoutine(name) },
            openRun: { [weak self] id in self?.openRun(id) },
            openHooks: { [weak self] in self?.routineWindows?.showHooks() },
            openHook: { [weak self] name, run in self?.routineWindows?.showHook(name, run: run) },
            openSetup: { [weak self] in self?.setupWindow?.show() })
        notifier.setUp(cli: cli) { [weak self] click in self?.show(click) }
        followStatus()
        updates.start()
        startScheduler()
    }

    // MARK: Opening and quitting

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
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        startScheduler()
        switch Lifecycle.reopenWindow(for: status) {
        case .setup: setupWindow?.show()
        case .settings: openSettings()
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

    // MARK: Settings

    private func openSettings() {
        guard let cli else { return }
        if settingsWindow == nil {
            let model = SettingsModel(cli: cli, openGroups: settingsOpenGroups)
            settingsModel = model
            let window = NSWindow(contentViewController: NSHostingController(rootView: SettingsView(model: model)))
            window.title = "\(AppInfo.name) Settings"
            window.isReleasedWhenClosed = false
            window.setContentSize(NSSize(width: 640, height: 680))
            window.center()
            window.delegate = self
            settingsWindow = window
        }
        settingsModel?.load()  // what changed elsewhere since it was last open
        bringToFront(settingsWindow)
    }

    func windowWillClose(_ notification: Notification) {
        guard let window = notification.object as? NSWindow, window === settingsWindow else { return }
        settingsOpenGroups = settingsModel?.expanded
        // Let go once the close is done, so the window, its views and the
        // settings are freed until it's next opened.
        DispatchQueue.main.async {
            guard self.settingsWindow?.isVisible == false else { return }  // reopened meanwhile
            self.settingsWindow = nil
            self.settingsModel = nil
        }
    }

    /// A click on one of the app's notifications: the run it's about, in
    /// its routine's (or hook's) window, or the app (the Routines window).
    private func show(_ click: Components.Schemas.ClickOutput) {
        if click.action == .show_hyphen_run, let routine = click.routine {
            if click.hook == true {
                routineWindows?.showHook(routine, run: click.run)
            } else {
                routineWindows?.showRoutine(routine, run: click.run)
            }
        } else {
            routineWindows?.showList()
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

    /// Shows the last status (the indicator, the routine windows) and
    /// delivers new notifications.
    private func reload() {
        indicator.show(status)
        routineWindows?.update(status)
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
                self.routineWindows?.update(self.status)
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

/// An alert in front of everything (the app has no windows of its own open).
@MainActor
func showAlert(_ title: String, _ text: String) {
    NSApp.activate()
    let alert = NSAlert()
    alert.messageText = title
    alert.informativeText = text
    alert.runModal()
}
