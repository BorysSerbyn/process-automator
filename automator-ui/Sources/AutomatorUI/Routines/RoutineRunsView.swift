import AutomatorUICore
import OpenAPIRuntime
import SwiftUI

/// The routine's run history, newest first: each run opens to what it did
/// (its final text, writes, tokens and cost), read from `runs show`.
struct RoutineRunsView: View {
    @ObservedObject var model: RoutineModel

    var body: some View {
        SettingsCard {
            CardTitle(title: "Runs", systemImage: "clock.arrow.circlepath") {
                if let runs = model.runs, !runs.isEmpty {
                    Text(runs.count == 1 ? "1 run" : "\(runs.count) runs")
                        .font(.subheadline)
                        .foregroundStyle(.tertiary)
                }
            }
            if let error = model.runsError {
                RowError(message: error)
                    .padding(.horizontal, SettingsMetrics.rowInset)
                    .padding(.bottom, 10)
            } else if let runs = model.runs {
                if runs.isEmpty {
                    Divider().padding(.leading, SettingsMetrics.rowInset)
                    Text("It hasn't run yet.")
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity)
                        .padding(20)
                }
                ForEach(runs, id: \.id) { run in
                    Divider().padding(.leading, SettingsMetrics.rowInset)
                    RunRow(
                        run: run, open: model.openRuns.contains(run.id), entry: model.runEntries[run.id],
                        error: model.runErrors[run.id], toggle: { model.toggleRun(run.id) },
                        openReport: { model.openReport(run.id) })
                }
            } else {
                ProgressView().controlSize(.small).frame(maxWidth: .infinity).padding(16)
            }
        }
    }
}

/// One run: when, how it ended, how many tool calls and what it cost; open,
/// what it did.
struct RunRow: View {
    let run: RunSummary
    let open: Bool
    let entry: RunEntry?
    let error: String?
    let toggle: () -> Void
    let openReport: () -> Void

    var body: some View {
        DisclosureRow(open: open, prominent: false, help: "this run", toggle: toggle) {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(Formats.when(run.startedAt)).font(.body.weight(.medium))
                    OutcomeTag(outcome: run.outcome)
                    Tag(text: run.trigger)
                }
                if let reason = run.reason, !reason.isEmpty {
                    Text(reason)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .lineLimit(open ? nil : 1)
                        .truncationMode(.tail)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            VStack(alignment: .trailing, spacing: 3) {
                if let calls = run.toolCalls {
                    Text(calls == 1 ? "1 tool call" : "\(calls) tool calls")
                }
                if let cost = run.costUsd { Text(Formats.cost(cost)) }
            }
            .font(.subheadline.monospacedDigit())
            .foregroundStyle(.secondary)
        }
        if open {
            RunDetail(entry: entry, error: error, openReport: openReport)
                .padding(.leading, 34)
                .padding(.trailing, SettingsMetrics.rowInset)
                .padding(.bottom, 14)
        }
    }
}

/// What a run did: the agent's final text, its writes, and its tokens.
struct RunDetail: View {
    let entry: RunEntry?
    let error: String?
    let openReport: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            if let error {
                RowError(message: error)
            } else if let entry {
                if let result = entry.result {
                    resultView(result)
                } else if let text = entry.reason ?? entry.error {
                    Text(text).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                }
                HStack {
                    if let result = entry.result {
                        Text(tokens(result))
                            .font(.caption.monospacedDigit())
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("Open Report", action: openReport)
                        .controlSize(.small)
                        .help("Open a readable report of this run")
                }
            } else {
                ProgressView().controlSize(.small)
            }
        }
    }

    @ViewBuilder private func resultView(_ result: Components.Schemas.Result) -> some View {
        if result.dryRun || result.outcome != nil {
            HStack(spacing: 6) {
                if let outcome = result.outcome { Tag(text: outcome) }
                if result.dryRun { Tag(text: "dry run", tint: .blue) }
            }
        }
        if !result.text.isEmpty {
            Text(markdown(result.text))
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                .padding(12)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(
                    Color.primary.opacity(0.035), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
        }
        if let calls = result.calls, !calls.isEmpty {
            ToolCallsView(calls: calls)
        } else if !result.writes.isEmpty {
            // Records from before every call was kept: only the writes.
            VStack(alignment: .leading, spacing: 8) {
                Text(result.writes.count == 1 ? "1 write" : "\(result.writes.count) writes")
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(.secondary)
                ForEach(Array(result.writes.enumerated()), id: \.offset) { _, write in
                    WriteView(write: write)
                }
            }
        }
    }

    private func tokens(_ result: Components.Schemas.Result) -> String {
        let n = { (value: Int) in value.formatted() }
        return "\(n(result.inputTokens)) in · \(n(result.outputTokens)) out · \(n(result.cachedTokens)) cached"
            + " · \(result.backend)"
    }

    /// The agent's text keeps its line breaks; **bold**, `code` and links show.
    private func markdown(_ text: String) -> AttributedString {
        (try? AttributedString(
            markdown: text, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)))
            ?? AttributedString(text)
    }
}

/// Every tool call a run made, in order: one row each, opening to its
/// arguments; writes are tinted and start open.
struct ToolCallsView: View {
    let calls: [Components.Schemas.Call]

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(heading)
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(.secondary)
            SettingsCard(nested: true) {
                ForEach(Array(calls.enumerated()), id: \.offset) { index, call in
                    if index > 0 { Divider().padding(.leading, 34) }
                    CallRow(number: index + 1, call: call)
                }
            }
        }
    }

    private var heading: String {
        let writes = calls.filter(\.write).count
        let total = calls.count == 1 ? "1 tool call" : "\(calls.count) tool calls"
        return writes == 0 ? total : "\(total), \(writes == 1 ? "1 write" : "\(writes) writes")"
    }
}

/// One tool call: its number, tool, whether it writes, and whether it
/// failed; open, its arguments and error.
struct CallRow: View {
    let number: Int
    let call: Components.Schemas.Call
    @State private var open: Bool?

    private var isOpen: Bool { open ?? call.write }

    private func toggle() { open = !isOpen }

    var body: some View {
        DisclosureRow(open: isOpen, prominent: false, help: "its arguments", toggle: toggle) {
            Text("\(number)")
                .font(.caption.monospacedDigit())
                .foregroundStyle(.tertiary)
                .frame(minWidth: 16, alignment: .trailing)
            Text(call.tool)
                .font(.callout.monospaced())
                .foregroundStyle(call.write ? Color.orange : Color.primary)
            if call.write { Tag(text: "write", tint: .orange) }
            Spacer(minLength: 8)
            if call.error?.isEmpty == false {
                Image(systemName: "exclamationmark.circle.fill").foregroundStyle(.red).help("It failed")
            }
        }
        if isOpen {
            VStack(alignment: .leading, spacing: 6) {
                ArgumentsView(args: call.args)
                if let error = call.error, !error.isEmpty { RowError(message: error) }
            }
            .padding(.leading, 58)
            .padding(.trailing, SettingsMetrics.rowInset)
            .padding(.bottom, 10)
        }
    }
}

/// A side-effecting tool call: the tool, its arguments, and its error if it
/// failed (for runs recorded before every call was kept).
struct WriteView: View {
    let write: Components.Schemas.Write

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Image(systemName: "square.and.pencil").foregroundStyle(.orange)
                Text(write.tool).font(.callout.monospaced().weight(.medium))
            }
            ArgumentsView(args: write.args)
            if let error = write.error, !error.isEmpty { RowError(message: error) }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.orange.opacity(0.06), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Color.orange.opacity(0.18)))
    }
}

/// A tool call's arguments by name: text as is, anything else as JSON.
struct ArgumentsView: View {
    let args: OpenAPIObjectContainer

    var body: some View {
        if arguments.isEmpty {
            Text("No arguments").font(.caption).foregroundStyle(.secondary)
        }
        ForEach(arguments, id: \.key) { argument in
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(argument.key)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .frame(width: 84, alignment: .leading)
                Text(argument.value)
                    .font(.caption.monospaced())
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private var arguments: [(key: String, value: String)] {
        guard let data = try? JSONEncoder().encode(args),
            let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return [] }
        return object.keys.sorted().map { key in
            let value = object[key]
            if let text = value as? String { return (key, text) }
            let json = value.flatMap {
                try? JSONSerialization.data(withJSONObject: $0, options: [.fragmentsAllowed, .sortedKeys])
            }
            return (key, json.map { String(decoding: $0, as: UTF8.self) } ?? "")
        }
    }
}
