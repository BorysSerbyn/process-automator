import AppKit
import AutomatorUICore
import SwiftUI

/// The Routines window and each routine's own window. Each is freed with its
/// data when it closes (SwiftUI holds a lot); reopening reads it all again.
/// New run status reaches whichever are open.
@MainActor
final class RoutineWindows: NSObject, NSWindowDelegate {
    private let cli: CLI
    private let openSettings: () -> Void
    private let openSetup: () -> Void
    private var status: RunStatus?
    private var list: (window: NSWindow, model: RoutinesModel)?
    private var details: [String: (window: NSWindow, model: RoutineModel)] = [:]

    init(cli: CLI, openSettings: @escaping () -> Void, openSetup: @escaping () -> Void) {
        self.cli = cli
        self.openSettings = openSettings
        self.openSetup = openSetup
    }

    func update(_ status: RunStatus?) {
        self.status = status
        list?.model.update(status)
        for detail in details.values { detail.model.update(status) }
    }

    func showList() {
        if list == nil {
            let model = RoutinesModel(cli: cli, status: status)
            let view = RoutinesListView(
                model: model,
                openRoutine: { [weak self] in self?.showRoutine($0) },
                openSettings: { [weak self] in self?.openSettings() },
                openSetup: { [weak self] in self?.openSetup() })
            let window = makeWindow(view, title: "Routines", size: NSSize(width: 600, height: 460))
            list = (window, model)
        }
        list?.model.load()
        bringToFront(list?.window)
    }

    /// Shows a routine's window, on one of its runs if given.
    func showRoutine(_ name: String, run: String? = nil) {
        if details[name] == nil {
            let model = RoutineModel(name: name, cli: cli, status: status)
            model.onDeleted = { [weak self] in
                self?.details[name]?.window.close()
                self?.list?.model.load()
            }
            let window = makeWindow(RoutineDetailView(model: model), title: name, size: NSSize(width: 680, height: 760))
            details[name] = (window, model)
            // Each routine's window opens a little below and right of the last.
            if let previous = NSApp.windows.last(where: { $0 !== window && $0.isVisible && $0.delegate === self }) {
                window.setFrameTopLeftPoint(
                    window.cascadeTopLeft(from: NSPoint(x: previous.frame.minX, y: previous.frame.maxY)))
            }
        }
        details[name]?.model.load()
        if let run { details[name]?.model.showRun(run) }
        bringToFront(details[name]?.window)
    }

    private func makeWindow<Content: View>(_ view: Content, title: String, size: NSSize) -> NSWindow {
        let window = NSWindow(contentViewController: NSHostingController(rootView: view))
        window.title = title
        window.isReleasedWhenClosed = false
        window.setContentSize(size)
        window.center()
        window.delegate = self
        return window
    }

    func windowWillClose(_ notification: Notification) {
        guard let window = notification.object as? NSWindow else { return }
        // Let go once the close is done, unless it was reopened meanwhile.
        DispatchQueue.main.async {
            guard !window.isVisible else { return }
            if self.list?.window === window { self.list = nil }
            if let name = self.details.first(where: { $0.value.window === window })?.key {
                self.details[name] = nil
            }
        }
    }
}

/// Shows a window in front, from a menu choice. It waits for the menu to
/// finish closing, and orders the window front even if macOS doesn't
/// activate the app (a menu bar app's activation is only a request), so the
/// first click never leaves the window behind the frontmost app.
@MainActor
func bringToFront(_ window: NSWindow?) {
    DispatchQueue.main.async {
        NSApp.activate()
        window?.makeKeyAndOrderFront(nil)
        window?.orderFrontRegardless()
    }
}
