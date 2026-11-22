import AppKit
import AutomatorUICore
import SwiftUI

/// The Routines window and each routine's own window, the Hooks window and
/// each hook's and watcher's. Each is freed with its data when it closes
/// (SwiftUI holds a lot); reopening reads it all again. New run status
/// reaches whichever are open.
@MainActor
final class RoutineWindows: NSObject, NSWindowDelegate {
    private let cli: CLI
    private let openSettings: () -> Void
    private let openSetup: () -> Void
    private var status: RunStatus?
    private var list: (window: NSWindow, model: RoutinesModel)?
    private var details: [String: (window: NSWindow, model: RoutineModel)] = [:]
    private var hooksList: (window: NSWindow, model: HooksModel)?
    private var hooks: [String: (window: NSWindow, model: HookModel)] = [:]
    private var watchers: [String: (window: NSWindow, model: WatcherModel)] = [:]

    init(cli: CLI, openSettings: @escaping () -> Void, openSetup: @escaping () -> Void) {
        self.cli = cli
        self.openSettings = openSettings
        self.openSetup = openSetup
    }

    func update(_ status: RunStatus?) {
        self.status = status
        list?.model.update(status)
        for detail in details.values { detail.model.update(status) }
        hooksList?.model.update(status)
        for hook in hooks.values { hook.model.update(status) }
        for watcher in watchers.values { watcher.model.update(status) }
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
            cascade(window)
        }
        details[name]?.model.load()
        if let run { details[name]?.model.showRun(run) }
        bringToFront(details[name]?.window)
    }

    /// Shows the Hooks window: every hook and watcher.
    func showHooks() {
        if hooksList == nil {
            let model = HooksModel(cli: cli, status: status)
            let view = HooksListView(
                model: model, openHook: { [weak self] in self?.showHook($0) },
                openWatcher: { [weak self] in self?.showWatcher($0) })
            hooksList = (makeWindow(view, title: "Hooks", size: NSSize(width: 620, height: 520)), model)
        }
        hooksList?.model.load()
        bringToFront(hooksList?.window)
    }

    /// Shows a hook's window, on one of its runs if given.
    func showHook(_ name: String, run: String? = nil) {
        if hooks[name] == nil {
            let model = HookModel(name: name, cli: cli, status: status)
            model.onDeleted = { [weak self] in
                self?.hooks[name]?.window.close()
                self?.hooksList?.model.load()
            }
            let view = HookDetailView(model: model, openWatcher: { [weak self] in self?.showWatcher($0) })
            let window = makeWindow(view, title: name, size: NSSize(width: 680, height: 760))
            hooks[name] = (window, model)
            cascade(window)
        }
        hooks[name]?.model.load()
        if let run { hooks[name]?.model.showRun(run) }
        bringToFront(hooks[name]?.window)
    }

    /// Shows a watcher's window.
    func showWatcher(_ name: String) {
        if watchers[name] == nil {
            let model = WatcherModel(name: name, cli: cli, status: status)
            model.onDeleted = { [weak self] in
                self?.watchers[name]?.window.close()
                self?.hooksList?.model.load()
            }
            let view = WatcherView(model: model, openHook: { [weak self] in self?.showHook($0) })
            let window = makeWindow(view, title: name, size: NSSize(width: 640, height: 680))
            watchers[name] = (window, model)
            cascade(window)
        }
        watchers[name]?.model.load()
        bringToFront(watchers[name]?.window)
    }

    /// A routine's, hook's or watcher's window opens a little below and
    /// right of the last.
    private func cascade(_ window: NSWindow) {
        if let previous = NSApp.windows.last(where: { $0 !== window && $0.isVisible && $0.delegate === self }) {
            window.setFrameTopLeftPoint(
                window.cascadeTopLeft(from: NSPoint(x: previous.frame.minX, y: previous.frame.maxY)))
        }
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
            if self.hooksList?.window === window { self.hooksList = nil }
            if let name = self.hooks.first(where: { $0.value.window === window })?.key { self.hooks[name] = nil }
            if let name = self.watchers.first(where: { $0.value.window === window })?.key {
                self.watchers[name] = nil
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
