import AppKit
import AutomatorUICore
import SwiftUI

/// The Setup window, freed with its data when it closes; reopening reads
/// where setup stands again.
@MainActor
final class SetupWindow: NSObject, NSWindowDelegate {
    private let cli: CLI
    private let openRoutine: (String) -> Void
    private let changed: () -> Void
    private var window: NSWindow?
    private var model: SetupViewModel?

    /// changed: an action succeeded (run status is up to date: show it).
    init(cli: CLI, openRoutine: @escaping (String) -> Void, changed: @escaping () -> Void) {
        self.cli = cli
        self.openRoutine = openRoutine
        self.changed = changed
    }

    func show() {
        if window == nil {
            let model = SetupViewModel(cli: cli, openRoutine: openRoutine, changed: changed)
            let window = NSWindow(contentViewController: NSHostingController(rootView: SetupView(model: model)))
            window.title = "Process Automator Setup"
            window.isReleasedWhenClosed = false
            window.setContentSize(NSSize(width: 640, height: 680))
            window.center()
            window.delegate = self
            self.window = window
            self.model = model
        }
        model?.load()
        bringToFront(window)
    }

    func windowWillClose(_ notification: Notification) {
        DispatchQueue.main.async {
            guard self.window?.isVisible == false else { return }  // reopened meanwhile
            self.window = nil
            self.model = nil
        }
    }
}
