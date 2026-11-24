import AppKit
import AutomatorUICore
import SwiftUI

/// Updates, as the core reports them: where they stand (`update status`, no
/// network: the scheduler checks every hour and installs by itself while
/// automatic updates are on), asked on launch, every minute and when the
/// menu opens; while one installs, what it's doing (`update progress`,
/// every 2 seconds). The menu's Manage Updates… line says when one is
/// available or installing, and opens Updates in the app's window. While
/// the app's window is open, or its menu is, automatic updates are held
/// back (`update postpone`): one never restarts the app under me. Whether
/// one is available, installing or waiting, and why, is the core's answer.
@MainActor
final class Updates: NSObject {
    private let cli: CLI?
    let model: ManageUpdatesModel
    private var timer: Timer?
    private var following: Timer?
    /// The menu's line while the menu is open, so a fresh answer shows at once.
    private weak var menuItem: NSMenuItem?
    /// Opens Updates in the app's window (the app sets it).
    var open: () -> Void = {}

    init(cli: CLI?) {
        self.cli = cli
        model = ManageUpdatesModel(cli: cli)
        super.init()
        model.changed = { [weak self] in self?.changed() }
    }

    func start() {
        model.refresh()
        timer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.model.refresh()
                if Self.windowOpen { self?.postpone() }
            }
        }
    }

    /// Whether the app's window is open (not the menu bar's menu).
    private static var windowOpen: Bool {
        NSApp.windows.contains { $0.isVisible && $0.styleMask.contains(.titled) }
    }

    /// Holds automatic updates back: the app is in use.
    private func postpone() {
        guard let cli else { return }
        Task { _ = await cli.background { $0.postponeUpdate() } }
    }

    /// Manage Updates…, saying when an update is installing or available.
    func makeMenuItem() -> NSMenuItem {
        let item = NSMenuItem(title: "Manage Updates…", action: #selector(show), keyEquivalent: "")
        item.target = self
        configure(item)
        menuItem = item
        model.refresh()
        postpone()
        return item
    }

    private func configure(_ item: NSMenuItem) {
        let note = model.menuNote
        if #available(macOS 14.4, *) {
            item.title = "Manage Updates…"
            item.subtitle = note
        } else {
            item.title = note.map { "Manage Updates… (\($0))" } ?? "Manage Updates…"
        }
        item.toolTip = model.installing ? "The app restarts when it's done." : nil
    }

    /// What the model learned: the menu line, and following an update while
    /// one installs.
    private func changed() {
        if let menuItem { configure(menuItem) }
        if model.installing { follow() }
    }

    /// Asks what the update is doing every 2 seconds, until it's over.
    private func follow() {
        guard following == nil else { return }
        following = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated {
                guard let self else { return }
                self.model.readProgress()
                if !self.model.installing {
                    self.following?.invalidate()
                    self.following = nil
                    self.model.refresh()
                }
            }
        }
        model.readProgress()
    }

    /// Manage Updates…: Updates in the app's window.
    @objc func show() { open() }

    /// Updates shown in the app's window: read again, and automatic
    /// updates held back.
    func opened() {
        model.load()
        postpone()
    }
}

/// What the Manage Updates window shows, as the core says it: where updates
/// stand, this version's notes, a check and what it found (with the newer
/// release's notes), and the update installing.
@MainActor
final class ManageUpdatesModel: ObservableObject {
    enum Notes: Equatable {
        case loading
        case loaded(Components.Schemas.NotesOutput)
        case failed(String)
    }

    enum Check: Equatable {
        case idle
        case checking
        case upToDate(current: String)
        case available(Components.Schemas.UpdateOutput)
        case failed(String)
    }

    private let cli: CLI?
    /// Called when what the menu shows may have changed.
    var changed: () -> Void = {}

    @Published var status: Components.Schemas.UpdateStatus?
    @Published var statusError: String?
    @Published var installedNotes: Notes = .loading
    @Published var check: Check = .idle
    @Published var availableNotes: Notes?
    @Published var progress: Components.Schemas.UpdateProgress?
    @Published var starting = false
    @Published var startError: String?
    /// Whether an update was seen installing while the app ran: only then
    /// is how it ended shown (one that failed long ago isn't news).
    @Published var sawInstalling = false
    @Published var savingAutomatic = false
    @Published var automaticError: String?
    private var refreshing = false
    private var reading = false

    init(cli: CLI?) { self.cli = cli }

    /// An update is installing (the core's status, or its progress).
    var installing: Bool { progress?.state == .installing || (progress == nil && status?.installing == true) }

    /// The menu's note: installing, or a newer release available.
    var menuNote: String? {
        if installing {
            let tag = progress?.tag ?? status?.installingTag ?? ""
            return tag.isEmpty ? "Updating…" : "Updating to \(tag)…"
        }
        if let status, status.updateAvailable, let latest = status.latest { return "\(latest) available" }
        return nil
    }

    /// Reads where updates stand again.
    func refresh() {
        guard let cli, !refreshing else { return }
        refreshing = true
        Task {
            let result = await cli.background { $0.updateStatus() }
            refreshing = false
            switch result {
            case .success(let answer):
                status = answer
                statusError = nil
                if answer.installing && progress?.state != .installing { readProgress() }
            case .failure(let failure): statusError = failure.message
            }
            changed()
        }
    }

    /// The window opening: where updates stand, this version's notes, and
    /// the update installing or the last one, if it failed.
    func load() {
        refresh()
        loadInstalledNotes()
        readProgress()
    }

    private func loadInstalledNotes() {
        guard let cli else { return }
        if case .loaded = installedNotes { return }
        installedNotes = .loading
        Task {
            switch await cli.background({ $0.releaseNotes() }) {
            case .success(let notes): installedNotes = .loaded(notes)
            case .failure(let failure): installedNotes = .failed(failure.message)
            }
        }
    }

    /// What the update installing is doing, or how the last one ended.
    func readProgress() {
        guard let cli, !reading else { return }
        reading = true
        Task {
            let result = await cli.background { $0.updateProgress() }
            reading = false
            if case .success(let answer) = result {
                progress = answer
                if answer.state == .installing { sawInstalling = true }
            }
            changed()
        }
    }

    /// Check for Updates: asks GitHub now; a newer release comes with its notes.
    func checkNow() {
        guard let cli, check != .checking else { return }
        check = .checking
        availableNotes = nil
        startError = nil
        Task {
            switch await cli.background({ $0.updateCheck() }) {
            case .failure(let failure):
                check = .failed(failure.message)
            case .success(let answer) where answer.inProgress:
                check = .idle
                readProgress()
            case .success(let answer) where answer.updateAvailable:
                check = .available(answer)
                availableNotes = .loading
                let tag = answer.latest
                switch await cli.background({ $0.releaseNotes(tag) }) {
                case .success(let notes): availableNotes = .loaded(notes)
                case .failure(let failure): availableNotes = .failed(failure.message)
                }
            case .success(let answer):
                check = .upToDate(current: answer.current)
            }
            refresh()
        }
    }

    /// Install Now: starts the newer release installing (`update --detach`);
    /// the app restarts when it's done.
    func installNow() {
        guard let cli, !starting, !installing else { return }
        starting = true
        startError = nil
        Task {
            let result = await cli.background { $0.startUpdate() }
            starting = false
            switch result {
            case .success(let answer) where answer.started == true || answer.inProgress:
                check = .idle
                sawInstalling = true
                readProgress()
            case .success(let answer):
                check = .upToDate(current: answer.current)
            case .failure(let failure):
                startError = failure.message
            }
            refresh()
        }
    }

    /// The toggle: the setting the core names, on or off.
    func setAutomatic(_ on: Bool) {
        guard let cli, let setting = status?.setting, !savingAutomatic else { return }
        savingAutomatic = true
        automaticError = nil
        Task {
            let result = await cli.background { $0.setSetting(setting, on ? "true" : "false") }
            savingAutomatic = false
            if case .failure(let failure) = result { automaticError = failure.message }
            refresh()
        }
    }
}

/// The Manage Updates window: Install updates automatically at the top;
/// the installed version with its release notes; then Check for Updates and
/// what it found (a newer release with its notes and Install Now, or up to
/// date), or the update installing.
struct ManageUpdatesView: View {
    @ObservedObject var model: ManageUpdatesModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header
                if let error = model.statusError {
                    Banner(
                        title: "Couldn't read where updates stand", message: error, systemImage: "xmark.octagon.fill",
                        tint: .red)
                }
                automaticCard
                installedCard
                updatesCard
            }
            .padding(24)
            .frame(maxWidth: 720)
            .frame(maxWidth: .infinity)
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 460, idealWidth: 560, minHeight: 420, idealHeight: 640)
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 12) {
            Image(nsImage: NSApp.applicationIconImage)
                .resizable()
                .frame(width: 44, height: 44)
            VStack(alignment: .leading, spacing: 3) {
                Text("Updates").font(.title2.weight(.semibold))
                Text("\(AppInfo.name) \(model.status?.current ?? AppInfo.version)")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
        }
    }

    // MARK: Automatic

    private var automaticCard: some View {
        SettingsCard {
            VStack(alignment: .leading, spacing: 8) {
                Toggle(isOn: automatic) {
                    VStack(alignment: .leading, spacing: 3) {
                        Text("Install updates automatically").font(.headline)
                        Text(
                            "New releases install by themselves once they've been out an hour, when no routine is running and the app's window isn't open. A notification tells you when one is installed."
                        )
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    }
                }
                .toggleStyle(.switch)
                .disabled(model.status == nil || model.savingAutomatic)
                if let status = model.status, status.automatic, let blocked = status.automaticBlocked {
                    note("exclamationmark.triangle.fill", .orange, "Not on this install: \(blocked).")
                } else if let waiting = model.status?.waiting {
                    note("clock", .secondary, waiting + ".")
                }
                if let error = model.automaticError {
                    note("xmark.octagon.fill", .red, error)
                }
            }
            // As wide as the cards below: its own content doesn't push it out.
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(16)
        }
    }

    private var automatic: Binding<Bool> {
        Binding(get: { model.status?.automatic ?? false }, set: { model.setAutomatic($0) })
    }

    private func note(_ symbol: String, _ tint: Color, _ text: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Image(systemName: symbol).foregroundStyle(tint)
            Text(text).fixedSize(horizontal: false, vertical: true).textSelection(.enabled)
        }
        .font(.callout)
    }

    // MARK: Installed

    private var installedCard: some View {
        SettingsCard {
            VStack(alignment: .leading, spacing: 10) {
                HStack(alignment: .firstTextBaseline) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Installed").font(.caption).foregroundStyle(.secondary).textCase(.uppercase)
                        Text("Version \(model.status?.current ?? AppInfo.version)").font(.headline)
                    }
                    Spacer()
                    if case .loaded(let notes) = model.installedNotes { releaseLink(notes) }
                }
                NotesBox(notes: model.installedNotes, maxHeight: 240)
            }
            .padding(16)
        }
    }

    private func releaseLink(_ notes: Components.Schemas.NotesOutput) -> some View {
        Group {
            if let link = notes.url, let url = URL(string: link) {
                Link("View on GitHub", destination: url).font(.callout)
            }
        }
    }

    // MARK: Checking and installing

    private var updatesCard: some View {
        SettingsCard {
            VStack(alignment: .leading, spacing: 12) {
                if model.installing {
                    installingView
                } else {
                    checkView
                }
                if !model.installing, model.sawInstalling, let p = model.progress, p.state == .failed {
                    Banner(
                        title: "The update\(p.tag.map { " to \($0)" } ?? "") failed",
                        message: [p.error, p.log.isEmpty ? nil : "Log: \(p.log)"].compactMap { $0 }.joined(
                            separator: "\n"),
                        systemImage: "xmark.octagon.fill", tint: .red)
                }
            }
            .padding(16)
        }
    }

    private var checkView: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .center, spacing: 12) {
                VStack(alignment: .leading, spacing: 3) {
                    Text(checkTitle).font(.headline)
                    if let detail = checkDetail {
                        Text(detail)
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                            .textSelection(.enabled)
                    }
                }
                Spacer(minLength: 12)
                Button {
                    model.checkNow()
                } label: {
                    if model.check == .checking {
                        HStack(spacing: 6) {
                            ProgressView().controlSize(.small)
                            Text("Checking…")
                        }
                    } else {
                        Text("Check for Updates")
                    }
                }
                .disabled(model.check == .checking || model.starting)
            }
            if case .available(let update) = model.check {
                Divider()
                HStack(alignment: .center, spacing: 12) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Available").font(.caption).foregroundStyle(.secondary).textCase(.uppercase)
                        Text(
                            "Version \(update.latest.hasPrefix("v") ? String(update.latest.dropFirst()) : update.latest)"
                        )
                        .font(.headline)
                    }
                    Spacer()
                    if case .loaded(let notes) = model.availableNotes { releaseLink(notes) }
                    Button {
                        model.installNow()
                    } label: {
                        if model.starting {
                            HStack(spacing: 6) {
                                ProgressView().controlSize(.small)
                                Text("Starting…")
                            }
                        } else {
                            Text("Install Now")
                        }
                    }
                    .keyboardShortcut(.defaultAction)
                    .disabled(model.starting)
                }
                if let notes = model.availableNotes { NotesBox(notes: notes, maxHeight: 200) }
                if let note = update.note {
                    Text(note).font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
                if let error = model.startError {
                    Banner(
                        title: "The update didn't start", message: error, systemImage: "xmark.octagon.fill", tint: .red)
                }
            }
        }
    }

    private var checkTitle: String {
        switch model.check {
        case .checking: "Checking for updates…"
        case .upToDate: "\(AppInfo.name) is up to date"
        case .available: "An update is available"
        case .failed: "Couldn't check for updates"
        case .idle:
            if let status = model.status, status.updateAvailable, let latest = status.latest {
                "\(latest) is available"
            } else {
                "Check for a newer release"
            }
        }
    }

    private var checkDetail: String? {
        switch model.check {
        case .failed(let error): return error
        case .upToDate(let current): return "\(current) is the latest release."
        case .checking, .available: return nil
        case .idle:
            guard let status = model.status, let checked = status.checkedAt else { return nil }
            var text = "Last checked \(Formats.when(checked).lowercasedFirst)"
            if let error = status.checkError { text += ", which failed: \(error)" }
            return text + "."
        }
    }

    private var installingView: some View {
        let p = model.progress
        let tag = p?.tag ?? model.status?.installingTag
        return VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 10) {
                Image(systemName: "arrow.down.circle").font(.title2).foregroundStyle(Color.accentColor)
                VStack(alignment: .leading, spacing: 2) {
                    Text(tag.map { "Updating to \($0)" } ?? "Updating").font(.headline)
                    if let started = p?.startedAt {
                        Text("Started \(Formats.when(started).lowercasedFirst)").font(.subheadline).foregroundStyle(
                            .secondary)
                    }
                }
                Spacer()
                Button("Check for Updates") {}.disabled(true)
            }
            ProgressView().progressViewStyle(.linear)
            Text(p?.step ?? "Starting…").fixedSize(horizontal: false, vertical: true)
            if let detail = p?.detail, !detail.isEmpty {
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
    }
}

/// A release's notes, readable and scrollable: headings, bullets and
/// paragraphs, links clickable, text selectable; loading, or why they
/// couldn't be read.
struct NotesBox: View {
    let notes: ManageUpdatesModel.Notes
    let maxHeight: CGFloat

    var body: some View {
        Group {
            switch notes {
            case .loading:
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Reading the release notes…").foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(12)
            case .failed(let error):
                Text(error)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(12)
            case .loaded(let loaded):
                let blocks = ReleaseNotes.blocks(loaded.notes)
                if blocks.isEmpty {
                    Text("This release has no notes.")
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(12)
                } else {
                    ScrollView {
                        VStack(alignment: .leading, spacing: 6) {
                            ForEach(Array(blocks.enumerated()), id: \.offset) { _, block in row(block) }
                        }
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(12)
                    }
                    .frame(maxHeight: maxHeight)
                    .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
        .font(.callout)
        .background(
            Color(nsColor: .textBackgroundColor).opacity(0.6),
            in: RoundedRectangle(cornerRadius: 8, style: .continuous)
        )
        .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Color.primary.opacity(0.08)))
    }

    @ViewBuilder private func row(_ block: ReleaseNotes.Block) -> some View {
        switch block {
        case .heading(let text):
            Text(ReleaseNotes.inline(text)).font(.headline).padding(.top, 4)
        case .bullet(let text, let depth):
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Text("•").foregroundStyle(.secondary)
                Text(ReleaseNotes.inline(text)).fixedSize(horizontal: false, vertical: true)
            }
            .padding(.leading, CGFloat(depth) * 16)
        case .paragraph(let text):
            Text(ReleaseNotes.inline(text)).fixedSize(horizontal: false, vertical: true)
        }
    }
}

extension String {
    /// "Today at 09:20" → "today at 09:20", for the middle of a sentence
    /// (a date, "19 Nov 2026 at 09:20", stays as it is).
    fileprivate var lowercasedFirst: String {
        ["Today", "Yesterday", "Tomorrow"].contains(where: hasPrefix) ? prefix(1).lowercased() + dropFirst() : self
    }
}
