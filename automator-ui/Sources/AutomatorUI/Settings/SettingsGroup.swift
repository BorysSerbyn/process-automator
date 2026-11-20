import AutomatorUICore
import SwiftUI

enum SettingsMetrics {
    /// The horizontal padding inside a card, for headers and rows alike.
    static let rowInset: CGFloat = 14
    static let cornerRadius: CGFloat = 10
}

/// A rounded group box, like a grouped Form's section.
struct SettingsCard<Content: View>: View {
    var nested = false
    @ViewBuilder let content: Content

    var body: some View {
        let shape = RoundedRectangle(cornerRadius: nested ? 8 : SettingsMetrics.cornerRadius, style: .continuous)
        VStack(alignment: .leading, spacing: 0) { content }
            .background(shape.fill(Color.primary.opacity(nested ? 0.035 : 0.045)))
            .overlay(shape.strokeBorder(Color.primary.opacity(nested ? 0.07 : 0.09)))
            .clipShape(shape)
    }
}

/// A top-level group (llm, update, …) as a card: its header, then, when open,
/// its settings and its subgroups (llm › budget) as nested boxes.
struct SettingsGroupView: View {
    let group: KeyTree.Group<SettingState>
    @ObservedObject var model: SettingsModel

    var body: some View {
        SettingsCard {
            GroupHeader(
                name: group.name, count: count(group), open: model.isOpen(group.id), prominent: true
            ) { model.toggle(group.id) }
            if model.isOpen(group.id) {
                SettingRows(entries: group.entries, errors: model.errors, set: model.set, reset: model.reset)
                ForEach(group.groups) { sub in
                    SettingsCard(nested: true) {
                        GroupHeader(
                            name: sub.name, count: count(sub), open: model.isOpen(sub.id), prominent: false
                        ) { model.toggle(sub.id) }
                        if model.isOpen(sub.id) {
                            SettingRows(
                                entries: sub.entries, errors: model.errors, set: model.set, reset: model.reset)
                        }
                    }
                    .padding(.horizontal, 10)
                    .padding(.top, group.entries.isEmpty ? 0 : 2)
                    .padding(.bottom, 10)
                }
            }
        }
    }

    private func count(_ group: KeyTree.Group<SettingState>) -> Int {
        group.entries.count + group.groups.map(count).reduce(0, +)
    }
}

/// A group's settings, one row each, with a hairline above every row (but
/// the first, when `dividerFirst` is false). App and routine settings alike.
struct SettingRows: View {
    let entries: [KeyTree.Entry<SettingState>]
    /// The core's message for each key it refused a value for.
    let errors: [String: String]
    let set: (_ key: String, _ text: String) -> Void
    let reset: (_ key: String) -> Void
    var dividerFirst = true

    var body: some View {
        ForEach(Array(entries.enumerated()), id: \.element.item.key) { index, entry in
            let setting = entry.item
            if dividerFirst || index > 0 { Divider().padding(.leading, SettingsMetrics.rowInset) }
            SettingRow(
                setting: setting, label: entry.label, error: errors[setting.key],
                set: { set(setting.key, $0) }, reset: { reset(setting.key) })
        }
    }
}

/// A group's name with its disclosure chevron; the whole row toggles it.
struct GroupHeader: View {
    let name: String
    /// How many settings the group holds, subgroups included.
    let count: Int
    let open: Bool
    /// A top-level group (a section) rather than a subgroup inside one.
    let prominent: Bool
    let toggle: () -> Void

    var body: some View {
        DisclosureRow(open: open, prominent: prominent, help: name, toggle: toggle) {
            Text(name)
                .font(prominent ? .title3.weight(.semibold) : .body.weight(.semibold))
            Spacer(minLength: 8)
            Text(count == 1 ? "1 setting" : "\(count) settings")
                .font(.subheadline)
                .foregroundStyle(.tertiary)
        }
    }
}

/// A row that opens and closes what's under it: a disclosure chevron, then
/// its content; the whole row is the button, and it highlights on hover.
struct DisclosureRow<Content: View>: View {
    let open: Bool
    var prominent = true
    /// What it shows or hides, for the tooltip ("Show …" / "Hide …").
    let help: String
    let toggle: () -> Void
    @ViewBuilder var content: Content
    @State private var hovering = false

    var body: some View {
        Button(action: toggle) {
            HStack(spacing: 8) {
                Image(systemName: "chevron.right")
                    .font(.system(size: prominent ? 11 : 9, weight: .bold))
                    .foregroundStyle(.secondary)
                    .rotationEffect(.degrees(open ? 90 : 0))
                    .frame(width: 12)
                content
            }
            .padding(.horizontal, SettingsMetrics.rowInset)
            .padding(.vertical, prominent ? 11 : 8)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(hovering ? Color.primary.opacity(0.05) : .clear)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .accessibilityAddTraits(.isHeader)
        .accessibilityValue(open ? "expanded" : "collapsed")
        .help(open ? "Hide \(help)" : "Show \(help)")
    }
}
