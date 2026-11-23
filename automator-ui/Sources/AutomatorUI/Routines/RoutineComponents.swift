import AutomatorUICore
import SwiftUI

typealias RoutineInfo = Components.Schemas.RoutineInfo
typealias RoutineDetail = Components.Schemas.RoutineDetail
typealias RoutineStatus = Components.Schemas.RunStatusRoutine
typealias RunSummary = Components.Schemas.Summary
typealias RunEntry = Components.Schemas.Entry
typealias ScheduleInfo = Components.Schemas.Info
typealias Memory = Components.Schemas.Memory
typealias TrustReview = Components.Schemas.TrustOutput
typealias ProgramInfo = Components.Schemas.ProgramInfo

extension RoutineDetail {
    /// A program (an executable), not an LLM routine: it has no servers,
    /// tools, model or memories.
    var isProgram: Bool { kind == .program }
}

extension RunStatus {
    /// A routine's entry, by name.
    func routine(_ name: String) -> RoutineStatus? { routines.first { $0.name == name } }
}

/// A routine's health as the core judged it: ok green, error red, anything
/// else (none: paused) grey.
struct HealthDot: View {
    let health: String?
    var size: CGFloat = 9

    var body: some View {
        Circle()
            .fill(color)
            .frame(width: size, height: size)
            .overlay(Circle().strokeBorder(Color.primary.opacity(0.12), lineWidth: 0.5))
            .accessibilityLabel(health ?? "unknown")
    }

    private var color: Color {
        switch health {
        case "ok": .green
        case "error": .red
        default: .gray
        }
    }
}

/// A small capsule label: an outcome, a trigger, "Not trusted".
struct Tag: View {
    let text: String
    var tint: Color = .secondary

    var body: some View {
        Text(text)
            .font(.caption.weight(.medium))
            .foregroundStyle(tint == .secondary ? Color.secondary : tint)
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .background((tint == .secondary ? Color.primary : tint).opacity(0.12), in: Capsule())
    }
}

/// A run's outcome in the core's word, tinted: success green, failed red,
/// running or queued blue, others plain.
struct OutcomeTag: View {
    let outcome: String

    var body: some View {
        Tag(text: outcome, tint: tint)
    }

    private var tint: Color {
        switch outcome {
        case "success": .green
        case "failed": .red
        case "running", "queued": .blue
        default: .secondary
        }
    }
}

/// A card's title row (not collapsible), with an optional accessory on the right.
struct CardTitle<Accessory: View>: View {
    let title: String
    var systemImage: String?
    @ViewBuilder var accessory: Accessory

    var body: some View {
        HStack(spacing: 8) {
            if let systemImage {
                Image(systemName: systemImage).foregroundStyle(.secondary).frame(width: 16)
            }
            Text(title).font(.headline)
            Spacer(minLength: 8)
            accessory
        }
        .padding(.horizontal, SettingsMetrics.rowInset)
        .padding(.vertical, 10)
    }
}

extension CardTitle where Accessory == EmptyView {
    init(_ title: String, systemImage: String? = nil) {
        self.init(title: title, systemImage: systemImage) { EmptyView() }
    }
}

/// A label on the left, its value on the right: one fact in a card.
struct InfoRow<Content: View>: View {
    let label: String
    @ViewBuilder var content: Content

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(label)
                .foregroundStyle(.secondary)
                .frame(width: 92, alignment: .leading)
            VStack(alignment: .leading, spacing: 4) { content }
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.horizontal, SettingsMetrics.rowInset)
        .padding(.vertical, 9)
    }
}

/// Names (servers, tools) as a wrapping row of small chips; `marked` ones
/// (tools that write) are tinted.
struct Chips: View {
    let names: [String]
    var marked: Set<String> = []
    var markTint: Color = .orange

    var body: some View {
        FlowLayout(spacing: 4) {
            ForEach(names, id: \.self) { name in
                let tint = marked.contains(name) ? markTint : Color.primary
                Text(name)
                    .font(.callout.monospaced())
                    .foregroundStyle(tint)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(tint.opacity(0.08), in: RoundedRectangle(cornerRadius: 5, style: .continuous))
            }
        }
    }
}

/// Lays its views out left to right, wrapping onto new lines.
struct FlowLayout: Layout {
    var spacing: CGFloat = 4

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let rows = arrange(subviews, width: proposal.width ?? .infinity)
        let width = rows.map { $0.width }.max() ?? 0
        let height = rows.map(\.height).reduce(0, +) + spacing * CGFloat(max(rows.count - 1, 0))
        return CGSize(width: proposal.width ?? width, height: height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var y = bounds.minY
        for row in arrange(subviews, width: bounds.width) {
            var x = bounds.minX
            for index in row.indices {
                let size = subviews[index].sizeThatFits(.unspecified)
                subviews[index].place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
                x += size.width + spacing
            }
            y += row.height + spacing
        }
    }

    private struct Row {
        var indices: [Int] = []
        var width: CGFloat = 0
        var height: CGFloat = 0
    }

    private func arrange(_ subviews: Subviews, width: CGFloat) -> [Row] {
        var rows = [Row()]
        for index in subviews.indices {
            let size = subviews[index].sizeThatFits(.unspecified)
            if !rows[rows.count - 1].indices.isEmpty, rows[rows.count - 1].width + spacing + size.width > width {
                rows.append(Row())
            }
            var row = rows[rows.count - 1]
            row.width += (row.indices.isEmpty ? 0 : spacing) + size.width
            row.height = max(row.height, size.height)
            row.indices.append(index)
            rows[rows.count - 1] = row
        }
        return rows
    }
}

/// Dates and money as the windows show them.
enum Formats {
    /// "Today at 09:20", "Yesterday at 17:02", "19 Nov 2026 at 09:20".
    static func when(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.dateStyle = .medium
        formatter.timeStyle = .short
        formatter.doesRelativeDateFormatting = true
        return formatter.string(from: date)
    }

    /// "Fri 20 Nov, 09:20": a scheduled time.
    static func slot(_ date: Date) -> String {
        date.formatted(.dateTime.weekday(.abbreviated).day().month(.abbreviated).hour().minute())
    }

    static func cost(_ usd: Double) -> String {
        usd.formatted(.currency(code: "USD").precision(.fractionLength(2)))
    }
}

/// A rounded window-wide note above the content: not trusted, not responding.
struct Banner<Actions: View>: View {
    let title: String
    let message: String?
    let systemImage: String
    var tint: Color = .orange
    @ViewBuilder var actions: Actions

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: systemImage)
                .font(.title3)
                .foregroundStyle(tint)
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.headline)
                if let message, !message.isEmpty {
                    Text(message)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            actions
        }
        .padding(12)
        .background(
            tint.opacity(0.1), in: RoundedRectangle(cornerRadius: SettingsMetrics.cornerRadius, style: .continuous)
        )
        .overlay(
            RoundedRectangle(cornerRadius: SettingsMetrics.cornerRadius, style: .continuous)
                .strokeBorder(tint.opacity(0.3)))
    }
}

extension Banner where Actions == EmptyView {
    init(title: String, message: String?, systemImage: String, tint: Color = .orange) {
        self.init(title: title, message: message, systemImage: systemImage, tint: tint) { EmptyView() }
    }
}

/// A scroll view as tall as what it holds, up to maxHeight, scrolling only
/// past that. A plain ScrollView has no height of its own, so in a sheet it
/// shrinks to a sliver; this one measures its content.
struct FittingScrollView<Content: View>: View {
    let maxHeight: CGFloat
    @ViewBuilder let content: Content
    @State private var height: CGFloat = 0

    var body: some View {
        ScrollView {
            content.background(
                GeometryReader { proxy in
                    Color.clear.preference(key: ContentHeight.self, value: proxy.size.height)
                })
        }
        .frame(height: min(max(height, 1), maxHeight))
        .onPreferenceChange(ContentHeight.self) { height = $0 }
    }
}

private struct ContentHeight: PreferenceKey {
    static let defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = max(value, nextValue()) }
}

/// A control's label: its title and icon, or, while one of `actions` is
/// pending (PendingAction), a small spinner and what it's doing
/// ("Starting…"); the control is disabled meanwhile by its window.
struct PendingLabel: View {
    @ObservedObject var pending: PendingAction
    let actions: [String]
    let title: String
    var systemImage: String?

    var body: some View {
        if let current = pending.pending, actions.contains(current.action) {
            HStack(spacing: 6) {
                ProgressView().controlSize(.small)
                Text(current.label)
            }
        } else if let systemImage {
            Label(title, systemImage: systemImage)
        } else {
            Text(title)
        }
    }
}
