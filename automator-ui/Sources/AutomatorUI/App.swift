import AppKit
import AutomatorUICore
import SwiftUI

/// automator-ui: the indicator (menu bar icon and menu), the routines and
/// settings windows, desktop notifications and updates. It shows what the
/// core says and runs what the core offers: it reads the core's files and
/// runs its CLI.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    private let cli = CLI.bundled
    private var indicator: Indicator!
    private var updates: Updates!
    private let notifier = Notifier()

    /// Where the core writes run status and notifications; asked from the CLI
    /// (nil until it answers).
    private var statusURL: URL?
    private var notificationsURL: URL?
    private var locating = false
    private var dirSource: DispatchSourceFileSystemObject?
    private var refreshTimer: Timer?

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
            // After a setup action, run status is read again at once (the core
            // has written it), so the dot and Finish Setup… follow without
            // waiting for the folder watch.
            setupWindow = SetupWindow(
                cli: cli, openRoutine: { [weak self] name in self?.routineWindows?.showRoutine(name) },
                changed: { [weak self] in self?.reload() })
            routineWindows = RoutineWindows(
                cli: cli, openSettings: { [weak self] in self?.openSettings() },
                openSetup: { [weak self] in self?.setupWindow?.show() })
        }
        indicator = Indicator(
            updates: updates,
            openSettings: { [weak self] in self?.openSettings() },
            openRoutines: { [weak self] in self?.routineWindows?.showList() },
            openRoutine: { [weak self] name in self?.routineWindows?.showRoutine(name) },
            openRun: { [weak self] id in self?.openRun(id) },
            openSetup: { [weak self] in self?.setupWindow?.show() })
        notifier.setUp(cli: cli)
        watchCoreFiles()
        updates.start()
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

    private func openRun(_ id: String) {
        guard let cli else { return }
        Task {
            if let failure = await cli.background({ $0.openRun(id) }) {
                showAlert("Couldn't open the run", failure.message)
            }
        }
    }

    // MARK: The core's files

    /// Re-reads run status (the indicator shows it) and delivers new
    /// notifications. Also runs on a timer, so an expired status (the core
    /// stopped responding) shows without a file change.
    private func reload() {
        guard let statusURL else {
            locateFiles()
            return
        }
        let status = RunStatus.load(from: statusURL)
        indicator.show(status)
        routineWindows?.update(status)
        if let notificationsURL { notifier.deliver(from: notificationsURL) }
    }

    /// Asks the core where its files are, then watches and reads them. Until
    /// it answers, the status is unknown; the refresh timer asks again.
    private func locateFiles() {
        guard !locating, let cli else {
            indicator.show(nil)
            return
        }
        locating = true
        Task {
            let paths = await cli.background { $0.paths() }
            locating = false
            if let paths {
                statusURL = URL(filePath: paths.runStatus)
                notificationsURL = URL(filePath: paths.notifications)
                if dirSource == nil { startDirSource() }
                reload()
            } else {
                indicator.show(nil)
            }
        }
    }

    /// The core replaces its files atomically (write + rename), so watch the
    /// folder, not the files. A 15s timer notices an expired status (the core
    /// stopped heartbeating), the folder not existing yet, or a missed event.
    private func watchCoreFiles() {
        refreshTimer = Timer.scheduledTimer(withTimeInterval: 15, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated {
                guard let self else { return }
                if self.dirSource == nil { self.startDirSource() }
                self.reload()
            }
        }
        reload()
    }

    private func startDirSource() {
        guard let dir = statusURL?.deletingLastPathComponent() else { return }
        let fd = open(dir.path, O_EVTONLY)
        guard fd >= 0 else { return }
        let source = DispatchSource.makeFileSystemObjectSource(
            fileDescriptor: fd, eventMask: [.write, .delete, .rename], queue: .main)
        source.setEventHandler { [weak self] in
            MainActor.assumeIsolated {
                guard let self else { return }
                if source.data.contains(.delete) || source.data.contains(.rename) {
                    source.cancel()  // folder itself went away; the timer re-arms
                    self.dirSource = nil
                }
                self.reload()
            }
        }
        source.setCancelHandler { close(fd) }
        source.resume()
        dirSource = source
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
