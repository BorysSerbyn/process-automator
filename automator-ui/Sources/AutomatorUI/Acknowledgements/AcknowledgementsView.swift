import AutomatorUICore
import SwiftUI

/// The Acknowledgements window: each notification a routine or hook asked
/// me to acknowledge, oldest first, as a card of its own: its title, what
/// sent it and when, its text (a long one folded, opening in place to all
/// of it), and a footer with Show Run and Acknowledge; Acknowledge All in
/// the header. Under them, Past acknowledgements: those I've acknowledged,
/// the last acknowledged first, as the same cards with when I acknowledged
/// each and Show Run but no Acknowledge; the most recent few, Show More for
/// older ones; not shown while there are none. Both are read again when run
/// status says what waits changed.
struct AcknowledgementsView: View {
    @ObservedObject var model: Acknowledgements
    /// Opens what sent one on its run: a routine's or hook's window.
    let openRun: (_ name: String, _ run: String?, _ hook: Bool) -> Void

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header
                if let error = model.error {
                    Banner(title: "Couldn't acknowledge", message: error, systemImage: "xmark.octagon.fill", tint: .red)
                }
                if let error = model.loadError {
                    Banner(
                        title: "Couldn't read what's waiting", message: error, systemImage: "xmark.octagon.fill",
                        tint: .red)
                } else if model.loaded && model.waiting.isEmpty {
                    AcknowledgementsEmpty()
                } else if !model.waiting.isEmpty {
                    ForEach(model.waiting, id: \.id) { note in
                        AcknowledgementCard(
                            note: note, working: model.acknowledging.contains(note.id),
                            acknowledge: { Task { await model.acknowledge(note.id) } },
                            openRun: note.routine.map { name in { openRun(name, note.run, note.hook == true) } })
                    }
                } else {
                    ProgressView().controlSize(.small).frame(maxWidth: .infinity).padding(24)
                }
                if model.loaded { pastSection }
            }
            .padding(24)
            .frame(maxWidth: 720)
            .frame(maxWidth: .infinity)
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 480, idealWidth: 600, minHeight: 320, idealHeight: 560)
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                Text("Acknowledgements").font(.title2.weight(.semibold))
                Text(subtitle)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 12)
            if model.waiting.count > 1 {
                Button {
                    Task { await model.acknowledgeAll() }
                } label: {
                    if model.acknowledging.contains("*") {
                        ProgressView().controlSize(.small)
                    } else {
                        Text("Acknowledge All")
                    }
                }
                .disabled(model.acknowledging.contains("*"))
                .help("Acknowledge every notification waiting")
            }
        }
    }

    /// Those I've acknowledged, under what waits: a heading, the most
    /// recent cards, and Show More while older ones are hidden. Nothing
    /// while there are none.
    @ViewBuilder private var pastSection: some View {
        if let error = model.pastError {
            Banner(
                title: "Couldn't read past acknowledgements", message: error, systemImage: "xmark.octagon.fill",
                tint: .red)
        } else if !model.past.isEmpty {
            VStack(alignment: .leading, spacing: 3) {
                Text("Past acknowledgements").font(.title3.weight(.semibold))
                Text("What you've acknowledged, the most recent first.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            .padding(.top, 12)
            ForEach(model.pastShowing, id: \.id) { note in
                AcknowledgementCard(
                    note: note, working: false, acknowledge: nil,
                    openRun: note.routine.map { name in { openRun(name, note.run, note.hook == true) } })
            }
            let older = model.past.count - model.pastShowing.count
            if older > 0 {
                Button {
                    withAnimation(.easeInOut(duration: 0.2)) { model.showMorePast() }
                } label: {
                    Label(
                        "Show \(min(older, Acknowledgements.pastMore)) More", systemImage: "chevron.down"
                    )
                    .labelStyle(.titleAndIcon)
                }
                .buttonStyle(.borderless)
                .frame(maxWidth: .infinity)
                .help("\(older) older acknowledgements")
            }
        }
    }

    private var subtitle: String {
        switch model.waiting.count {
        case 0: "Notifications a routine or hook asks you to acknowledge wait here."
        case 1: "1 waiting. The dot stays red until you acknowledge it."
        default: "\(model.waiting.count) waiting. The dot stays red until you acknowledge them."
        }
    }
}

/// Nothing waits: said plainly, in a card like the others.
struct AcknowledgementsEmpty: View {
    var body: some View {
        SettingsCard {
            VStack(spacing: 8) {
                Image(systemName: "checkmark.circle.fill")
                    .font(.system(size: 34))
                    .foregroundStyle(.green)
                Text("All caught up").font(.headline)
                Text("When a routine or hook asks you to acknowledge something, it shows here.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 36)
            .padding(.horizontal, 24)
        }
    }
}

/// One notification waiting, as a card: an icon, its title and what sent it
/// and when; its text, full width under them, at a readable line length,
/// paragraphs and bullets as written (plain text, selectable), a long one
/// folded to its first lines; and a footer: Show more (a long text), then
/// Show Run and Acknowledge at the right, apart from the text. One I've
/// acknowledged (no acknowledge) has a muted check for its icon, says when I
/// acknowledged it, and has no Acknowledge (and no footer when there's
/// nothing for one).
struct AcknowledgementCard: View {
    let note: CoreNotification
    let working: Bool
    let acknowledge: (() -> Void)?
    let openRun: (() -> Void)?
    @State private var expanded: Bool

    /// Lines a long text shows while folded.
    static let foldedLines = 5

    init(
        note: CoreNotification, working: Bool, acknowledge: (() -> Void)?, openRun: (() -> Void)?,
        expanded: Bool = false
    ) {
        self.note = note
        self.working = working
        self.acknowledge = acknowledge
        self.openRun = openRun
        _expanded = State(initialValue: expanded)
    }

    private var bodyText: String { note.body?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "" }

    /// Whether the text is long enough to fold: more lines, or more words,
    /// than the folded card shows.
    private var folds: Bool {
        bodyText.count > 280
            || bodyText.split(separator: "\n", omittingEmptySubsequences: false).count > Self.foldedLines
    }

    var body: some View {
        SettingsCard {
            VStack(alignment: .leading, spacing: 12) {
                heading
                if !bodyText.isEmpty { text }
            }
            .padding(16)
            if acknowledge != nil || openRun != nil || folds {
                Divider()
                footer
                    .padding(.horizontal, 16)
                    .padding(.vertical, 10)
            }
        }
    }

    /// When I acknowledged one, as a phrase: "Acknowledged today at 9:05",
    /// "Acknowledged Nov 20, 2026 at 9:05".
    static func acknowledged(_ date: Date) -> String {
        var when = Formats.when(date)
        for day in ["Today", "Yesterday"] where when.hasPrefix(day) {
            when = day.lowercased() + when.dropFirst(day.count)
        }
        return "Acknowledged \(when)"
    }

    /// The icon's symbol and tint: a check for one acknowledged, else a
    /// bell (red when urgent).
    private var symbol: (name: String, tint: Color) {
        if acknowledge == nil { return ("checkmark", .secondary) }
        return note.urgent == true ? ("exclamationmark.bubble.fill", .red) : ("bell.badge.fill", .orange)
    }

    private var heading: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: symbol.name)
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(symbol.tint)
                .frame(width: 30, height: 30)
                .background(
                    (acknowledge == nil ? Color.primary.opacity(0.07) : symbol.tint.opacity(0.12)), in: Circle())
            VStack(alignment: .leading, spacing: 3) {
                Text(verbatim: note.title)
                    .font(.headline)
                    .fixedSize(horizontal: false, vertical: true)
                HStack(spacing: 6) {
                    if let routine = note.routine {
                        Text(verbatim: routine).font(.subheadline.monospaced())
                        Text("·")
                    }
                    if acknowledge == nil, let acked = note.acknowledgedAt {
                        Text(Self.acknowledged(acked))
                    } else {
                        Text(Formats.when(note.at))
                    }
                    if note.urgent == true { Tag(text: "Urgent", tint: .red) }
                }
                .font(.subheadline)
                .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    /// The text under the heading, lined up with the title.
    /// Folded, it's one block cut after its first lines; open, it's its
    /// paragraphs as written, a bulleted one (•, -, *) hanging after its
    /// bullet so wrapped lines line up.
    @ViewBuilder private var text: some View {
        Group {
            if folds && !expanded {
                Text(verbatim: bodyText)
                    .lineLimit(Self.foldedLines)
                    .truncationMode(.tail)
            } else {
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(Array(Self.paragraphs(bodyText).enumerated()), id: \.offset) { _, paragraph in
                        if let bullet = paragraph.bullet {
                            HStack(alignment: .firstTextBaseline, spacing: 6) {
                                Text(verbatim: bullet).foregroundStyle(.secondary)
                                Text(verbatim: paragraph.text).fixedSize(horizontal: false, vertical: true)
                            }
                        } else if paragraph.text.isEmpty {
                            Spacer().frame(height: 2)
                        } else {
                            Text(verbatim: paragraph.text).fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
            }
        }
        .font(.body)
        .lineSpacing(3)
        .textSelection(.enabled)
        .fixedSize(horizontal: false, vertical: true)
        .frame(maxWidth: 620, alignment: .leading)
        .padding(.leading, 42)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// A text's lines, each with its bullet when it starts with one.
    static func paragraphs(_ text: String) -> [(bullet: String?, text: String)] {
        text.split(separator: "\n", omittingEmptySubsequences: false).map { raw in
            let line = raw.trimmingCharacters(in: .whitespaces)
            for bullet in ["•", "-", "*", "–"] where line.hasPrefix(bullet + " ") {
                return ("•", String(line.dropFirst(bullet.count + 1)))
            }
            return (nil, line)
        }
    }

    private var footer: some View {
        HStack(spacing: 10) {
            if folds {
                Button {
                    withAnimation(.easeInOut(duration: 0.2)) { expanded.toggle() }
                } label: {
                    Label(expanded ? "Show Less" : "Show More", systemImage: expanded ? "chevron.up" : "chevron.down")
                        .labelStyle(.titleAndIcon)
                }
                .buttonStyle(.borderless)
                .help(expanded ? "Fold the text to its first lines" : "Show all of the text")
            }
            Spacer(minLength: 12)
            if let openRun {
                Button("Show Run", action: openRun)
                    .help("Open what sent it, on this run")
            }
            if let acknowledge {
                Button(action: acknowledge) {
                    if working {
                        ProgressView().controlSize(.small).frame(minWidth: 84)
                    } else {
                        Text("Acknowledge").frame(minWidth: 84)
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(working)
                .help("It stops waiting; once none waits, the dot is no longer red for it")
            }
        }
        .controlSize(.regular)
    }
}
