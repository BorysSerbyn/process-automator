import AppKit
import AutomatorUICore
import QuartzCore

/// The indicator: the app's glyph in the menu bar with a status dot (pulsing
/// while a routine runs), and its menu: the version, the status in the core's
/// words, Finish Setup… while setup isn't done; Routines › (each routine with
/// its status), Hooks › (each hook with its status, while there are any),
/// updates, Settings; Quit.
@MainActor
final class Indicator: NSObject, NSMenuDelegate {
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
    private let dotView = NSImageView()
    private let updates: Updates
    private let quit: () -> Void
    private let openSettings: () -> Void
    private let openRoutines: () -> Void
    private let openRoutine: (String) -> Void
    private let openRun: (String) -> Void
    private let openHooks: () -> Void
    private let openHook: (String, String?) -> Void
    private let openSetup: () -> Void
    /// The latest run status, shown as the core wrote it.
    private var status: RunStatus?

    /// Icon frame in points; matches the 18×18 viewBox of the SVG assets.
    private static let iconSize = NSSize(width: 18, height: 18)
    private static let pulseKey = "pulse"

    init(
        updates: Updates, quit: @escaping () -> Void, openSettings: @escaping () -> Void,
        openRoutines: @escaping () -> Void,
        openRoutine: @escaping (String) -> Void, openRun: @escaping (String) -> Void,
        openHooks: @escaping () -> Void, openHook: @escaping (String, String?) -> Void,
        openSetup: @escaping () -> Void
    ) {
        self.openSetup = openSetup
        self.openHooks = openHooks
        self.openHook = openHook
        self.updates = updates
        self.quit = quit
        self.openSettings = openSettings
        self.openRoutines = openRoutines
        self.openRoutine = openRoutine
        self.openRun = openRun
        super.init()
        // Where it sits is the user's (⌘-drag); macOS remembers it under this
        // name, so it stays put across updates and restarts.
        statusItem.autosaveName = "indicator"
        configureIcon()
        let menu = NSMenu()
        menu.delegate = self  // rebuilt each time it opens
        menu.minimumWidth = MenuTextView.menuWidth
        statusItem.menu = menu
    }

    /// Shows a run status (nil: the core's status couldn't be read).
    func show(_ status: RunStatus?) {
        self.status = status
        setLight(RunStatus.light(for: status))
        setPulsing(RunStatus.pulsing(for: status))
    }

    // MARK: Menu

    func menuNeedsUpdate(_ menu: NSMenu) {
        guard menu === statusItem.menu else { return }  // the Routines submenu is built with it
        menu.removeAllItems()
        menu.addItem(textItem(MenuTextView(text: "\(AppInfo.name) \(AppInfo.version)", lines: 1, dimmed: true)))
        for item in statusItems() { menu.addItem(item) }
        // Finish Setup… while run status names setup steps still to do (the
        // Routines window always has Setup).
        if RunStatus.responding(status), status?.setupLeft?.isEmpty == false {
            let setup = NSMenuItem(title: "Finish Setup…", action: #selector(setupChosen), keyEquivalent: "")
            setup.target = self
            menu.addItem(setup)
        }
        menu.addItem(.separator())
        menu.addItem(routinesItem())
        if let hooks = hooksItem() { menu.addItem(hooks) }
        menu.addItem(updates.makeMenuItem())
        let settings = NSMenuItem(title: "Settings", action: #selector(settingsChosen), keyEquivalent: ",")
        settings.target = self
        menu.addItem(settings)
        menu.addItem(.separator())
        let quit = NSMenuItem(title: "Quit", action: #selector(quitChosen), keyEquivalent: "q")
        quit.target = self
        menu.addItem(quit)
    }

    @objc private func settingsChosen() { openSettings() }

    /// Quit stops the scheduler too, then quits (the app's quit).
    @objc private func quitChosen() { quit() }

    /// Takes the icon out of the menu bar (quitting).
    func hide() { statusItem.isVisible = false }

    @objc private func setupChosen() { openSetup() }

    /// Routines ›: All Routines… (a menu item with a submenu can't be chosen
    /// itself), then every routine with its health and summary as the core
    /// gives them; choosing one opens its window.
    private func routinesItem() -> NSMenuItem {
        let submenu = NSMenu(title: "Routines")
        submenu.minimumWidth = MenuTextView.menuWidth
        submenu.delegate = self  // highlights its routine rows
        let all = NSMenuItem(title: "All Routines…", action: #selector(routinesChosen), keyEquivalent: "")
        all.target = self
        submenu.addItem(all)
        submenu.addItem(.separator())
        if let status, !status.routines.isEmpty {
            for routine in status.routines {
                let item = NSMenuItem(title: routine.name, action: #selector(routineChosen(_:)), keyEquivalent: "")
                item.target = self
                item.representedObject = routine.name
                item.view = RoutineMenuView(name: routine.name, summary: routine.summary, health: routine.health) {
                    [weak self] in self?.openRoutine(routine.name)
                }
                submenu.addItem(item)
            }
        } else {
            let none = status == nil ? "Not responding" : "No routines"
            submenu.addItem(textItem(MenuTextView(text: none, lines: 1, dimmed: true)))
        }
        let item = NSMenuItem(title: "Routines", action: nil, keyEquivalent: "")
        item.submenu = submenu
        return item
    }

    @objc private func routinesChosen() { openRoutines() }

    /// Hooks ›, while run status has hooks: All Hooks…, then every hook with
    /// its health and summary as the core gives them; choosing one opens its
    /// window. Hooks aren't routines: they never show under Routines ›.
    private func hooksItem() -> NSMenuItem? {
        guard let hooks = status?.hooks, !hooks.isEmpty else { return nil }
        let submenu = NSMenu(title: "Hooks")
        submenu.minimumWidth = MenuTextView.menuWidth
        submenu.delegate = self
        let all = NSMenuItem(title: "All Hooks…", action: #selector(hooksChosen), keyEquivalent: "")
        all.target = self
        submenu.addItem(all)
        submenu.addItem(.separator())
        for hook in hooks {
            let item = NSMenuItem(title: hook.name, action: #selector(hookChosen(_:)), keyEquivalent: "")
            item.target = self
            item.representedObject = hook.name
            item.view = RoutineMenuView(name: hook.name, summary: hook.summary, health: hook.health) {
                [weak self] in self?.openHook(hook.name, nil)
            }
            submenu.addItem(item)
        }
        let item = NSMenuItem(title: "Hooks", action: nil, keyEquivalent: "")
        item.submenu = submenu
        return item
    }

    @objc private func hooksChosen() { openHooks() }

    @objc private func hookChosen(_ item: NSMenuItem) {
        if let name = item.representedObject as? String { openHook(name, nil) }
    }

    /// A routine chosen from the keyboard (a click goes through its view).
    @objc private func routineChosen(_ item: NSMenuItem) {
        if let name = item.representedObject as? String { openRoutine(name) }
    }

    /// Routine rows draw their own highlight: redraw them as it moves.
    func menu(_ menu: NSMenu, willHighlight item: NSMenuItem?) {
        for row in menu.items { row.view?.needsDisplay = true }
    }

    /// What the status is, in the core's words: why it's red (each reason
    /// opens the run it's about; one about a hook, the hook's window, on its
    /// run if it names one, else on its Turn On review if it needs one; one
    /// about a routine with no run, the routine's), or its summary.
    private func statusItems() -> [NSMenuItem] {
        guard RunStatus.responding(status), let status else {
            return [NSMenuItem(title: "Not responding", action: nil, keyEquivalent: "")]
        }
        if let reasons = status.reasons, !reasons.isEmpty {
            return reasons.map { reason in
                var click: (() -> Void)?
                if reason.hook == true, let hook = reason.routine, !hook.isEmpty {
                    click = { [weak self] in self?.openHook(hook, reason.run) }
                } else if let id = reason.run {
                    click = { [weak self] in self?.openRun(id) }
                } else if let routine = reason.routine, !routine.isEmpty {
                    // Nothing ran (it isn't trusted, say): its window, on
                    // its Turn On review when it needs turning on.
                    click = { [weak self] in self?.openRoutine(routine) }
                }
                return textItem(
                    MenuTextView(
                        text: reason.text, detail: reason.detail, lines: 2, dimmed: click == nil, onClick: click))
            }
        }
        return [NSMenuItem(title: status.summary ?? "", action: nil, keyEquivalent: "")]
    }

    private func textItem(_ view: NSView) -> NSMenuItem {
        let item = NSMenuItem()
        item.view = view
        return item
    }

    // MARK: Icon

    /// The glyph is a template image (macOS tints it for light/dark menu bars, and its
    /// knockout ring keeps the dot legible); the dot is a separate, coloured layer on top.
    private func configureIcon() {
        guard let button = statusItem.button else { return }
        let glyph = Self.loadSVG("glyph")
        glyph?.isTemplate = true
        button.image = glyph
        button.imageScaling = .scaleNone
        button.setAccessibilityLabel("\(AppInfo.name) status")

        dotView.image = Self.loadSVG("dot") ?? Self.fallbackDot()
        dotView.imageScaling = .scaleNone
        dotView.translatesAutoresizingMaskIntoConstraints = false
        button.addSubview(dotView)
        NSLayoutConstraint.activate([
            dotView.centerXAnchor.constraint(equalTo: button.centerXAnchor),
            dotView.centerYAnchor.constraint(equalTo: button.centerYAnchor),
            dotView.widthAnchor.constraint(equalToConstant: Self.iconSize.width),
            dotView.heightAnchor.constraint(equalToConstant: Self.iconSize.height),
        ])
    }

    private func setLight(_ light: RunStatus.Light) {
        dotView.contentTintColor =
            switch light {
            case .green: .systemGreen
            case .red: .systemRed
            case .grey: .systemGray
            }
    }

    /// Fades the dot in and out while a routine runs.
    private func setPulsing(_ on: Bool) {
        dotView.wantsLayer = true
        guard let layer = dotView.layer else { return }
        let active = layer.animation(forKey: Self.pulseKey) != nil
        if on, !active {
            let pulse = CABasicAnimation(keyPath: "opacity")
            pulse.fromValue = 1.0
            pulse.toValue = 0.25
            pulse.duration = 0.8
            pulse.autoreverses = true
            pulse.repeatCount = .infinity
            pulse.timingFunction = CAMediaTimingFunction(name: .easeInEaseOut)
            layer.add(pulse, forKey: Self.pulseKey)
        } else if !on, active {
            layer.removeAnimation(forKey: Self.pulseKey)
        }
    }

    /// Loads an SVG from the app bundle's Resources at icon size, or nil when running
    /// outside the bundle (e.g. `swift run`).
    private static func loadSVG(_ name: String) -> NSImage? {
        guard let url = Bundle.main.url(forResource: name, withExtension: "svg"),
            let image = NSImage(contentsOf: url)
        else { return nil }
        image.size = iconSize
        image.isTemplate = true  // tinted via contentTintColor / the menu bar appearance
        return image
    }

    private static func fallbackDot() -> NSImage? {
        NSImage(systemSymbolName: "circle.fill", accessibilityDescription: nil)
    }
}
