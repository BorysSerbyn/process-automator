import AutomatorUICore
import SwiftUI

/// LLM usage against its limit, as `usage` reports it for the backend in use:
/// on a Claude plan (`plan`), the plan's usage in each window against
/// llm.usage-limit, with when it resets, as the core last read it from
/// Claude Code; with an API key (`budget`), spend
/// against each budget; on a backend with no limit in the app (`none`), that.
/// The core decides what's over; the bars only draw its numbers, and the app
/// never works out a cost itself.
struct UsageView: View {
    let report: Components.Schemas.Report?
    let error: String?

    var body: some View {
        CardTitle(title: "LLM usage", systemImage: "gauge.with.dots.needle.33percent") {
            if let report, report.kind == .plan, let limit = report.limitPercent {
                Text("Limit: \(limit.formatted())% of the plan")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
        }
        Divider().padding(.leading, SettingsMetrics.rowInset)
        if let report {
            switch report.kind {
            case .plan: plan(report)
            case .budget:
                ForEach(report.windows, id: \.name) { window in
                    BudgetUsageRow(window: window)
                }
            case .none:
                Text("No usage limit with this backend: its usage can't be read, and its runs have no cost.")
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, SettingsMetrics.rowInset)
                    .padding(.vertical, 12)
            }
            if report.over, let reason = report.reason, !reason.isEmpty {
                RowError(message: reason)
                    .padding(.horizontal, SettingsMetrics.rowInset)
                    .padding(.bottom, 10)
            }
        } else if let error {
            RowError(message: error)
                .padding(.horizontal, SettingsMetrics.rowInset)
                .padding(.vertical, 10)
        } else {
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text("Reading usage…").foregroundStyle(.secondary)
            }
            .padding(.horizontal, SettingsMetrics.rowInset)
            .padding(.vertical, 12)
        }
    }

    /// The plan's windows (or, before any reading, "Not read yet"), single
    /// models' weekly usage, when it was read, and why the last read failed.
    @ViewBuilder private func plan(_ report: Components.Schemas.Report) -> some View {
        let known =
            report.fetchedAt != nil || report.recordedAt != nil || report.windows.contains { $0.usedPercent != nil }
        if known {
            ForEach(report.windows, id: \.name) { window in
                PlanUsageRow(window: window, limit: report.limitPercent)
            }
        } else {
            Text("Not read yet.")
                .foregroundStyle(.secondary)
                .padding(.horizontal, SettingsMetrics.rowInset)
                .padding(.vertical, 12)
        }
        VStack(alignment: .leading, spacing: 4) {
            if let models = report.models, !models.isEmpty {
                ForEach(models, id: \.model) { model in
                    Text(
                        "\(model.model) weekly: \(model.usedPercent.formatted(.number.precision(.fractionLength(0))))% "
                            + "used, resets \(Formats.when(model.resetsAt))")
                }
                Text("Single-model limits are shown only; runs don't stop on them.")
                    .foregroundStyle(.tertiary)
            }
            // As of the newer of the last read and the last status line recording.
            if let latest = [report.fetchedAt, report.recordedAt].compactMap({ $0 }).max() {
                Text("As of \(Formats.when(latest)).")
            }
            if let failed = report.readError, !failed.isEmpty {
                Label("Couldn't read usage just now: \(failed)", systemImage: "exclamationmark.circle")
            }
        }
        .font(.subheadline)
        .foregroundStyle(.secondary)
        .fixedSize(horizontal: false, vertical: true)
        .padding(.leading, SettingsMetrics.rowInset + 122)
        .padding(.trailing, SettingsMetrics.rowInset)
        .padding(.bottom, 12)
    }
}

/// A window's name as the core gives it, with its length: "Session (5h)".
private func windowLabel(_ window: Components.Schemas.Window) -> String {
    "\(window.name.prefix(1).uppercased())\(window.name.dropFirst()) (\(window.length))"
}

/// One plan window: how much of the plan is used, against the usage limit
/// (the tick), and when it resets; or "Not known".
struct PlanUsageRow: View {
    let window: Components.Schemas.Window
    let limit: Double?

    var body: some View {
        UsageRowLayout(label: windowLabel(window)) {
            UsageBar(share: window.usedPercent.map { $0 / 100 }, tick: limit.map { $0 / 100 }, over: window.over)
        } amount: {
            Text(
                window.usedPercent.map { "\($0.formatted(.number.precision(.fractionLength(0))))% used" } ?? "Not known"
            )
            .foregroundStyle(window.over ? Color.red : window.usedPercent == nil ? Color.secondary : Color.primary)
        } caption: {
            if let resets = window.resetsAt {
                Text("Resets \(Formats.when(resets)).")
            }
        }
    }
}

/// One budget window: what runs spent against its budget (the budget is the
/// limit).
struct BudgetUsageRow: View {
    let window: Components.Schemas.Window

    private var spent: Double { window.spentUsd ?? 0 }
    private var budget: Double { window.budgetUsd ?? 0 }

    var body: some View {
        UsageRowLayout(label: windowLabel(window)) {
            UsageBar(share: budget > 0 ? spent / budget : nil, tick: nil, over: window.over)
        } amount: {
            Text(budget > 0 ? "\(Formats.cost(spent)) of \(Formats.cost(budget))" : Formats.cost(spent))
                .foregroundStyle(window.over ? Color.red : Color.primary)
        } caption: {
            if budget == 0 { Text("No budget: spend here never stops runs.") }
        }
    }
}

/// A usage row: label, bar and amount on a line, a caption under them.
struct UsageRowLayout<Bar: View, Amount: View, Caption: View>: View {
    let label: String
    @ViewBuilder var bar: Bar
    @ViewBuilder var amount: Amount
    @ViewBuilder var caption: Caption

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 12) {
                Text(label).frame(width: 110, alignment: .leading)
                bar
                amount
                    .font(.body.monospacedDigit())
                    .frame(width: 160, alignment: .trailing)
            }
            caption
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .padding(.leading, 122)
        }
        .padding(.horizontal, SettingsMetrics.rowInset)
        .padding(.vertical, 10)
    }
}

/// A share of a limit as a bar (empty when not known), with an optional tick.
struct UsageBar: View {
    /// 0…1, or nil when not known.
    let share: Double?
    let tick: Double?
    let over: Bool

    var body: some View {
        GeometryReader { geometry in
            let width = geometry.size.width
            let clamp = { (value: Double) in CGFloat(min(max(value, 0), 1)) }
            ZStack(alignment: .leading) {
                Capsule().fill(Color.primary.opacity(share == nil ? 0.05 : 0.08))
                if let share {
                    Capsule()
                        .fill(over ? Color.red : Color.accentColor)
                        .frame(width: max(clamp(share) * width, share > 0 ? 6 : 0))
                }
                if let tick {
                    Rectangle()
                        .fill(Color.primary.opacity(0.5))
                        .frame(width: 2, height: 14)
                        .offset(x: clamp(tick) * width - 1)
                }
            }
            .frame(height: 8)
            .frame(maxHeight: .infinity)
        }
        .frame(height: 14)
        .accessibilityElement()
        .accessibilityLabel(share.map { "\(Int(($0 * 100).rounded()))% used" } ?? "Not known")
    }
}
