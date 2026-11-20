import AutomatorUICore
import SwiftUI

/// A routine's schedule: in plain words with its next times, edited as days
/// of the week and a time, or as cron when that can't say it. The core does
/// all the reading and writing of cron (`schedule describe` / `build`); a
/// change shows its words first and is set on Save.
struct ScheduleEditor: View {
    /// The schedule setting (type cron).
    let setting: SettingState
    /// The current schedule, described by the core (nil when there's none).
    let current: ScheduleInfo?
    let cli: CLI
    let error: String?
    let save: (_ cron: String) -> Void
    let reset: () -> Void

    enum Mode: String, CaseIterable, Identifiable {
        case days = "Days and time"
        case custom = "Custom"
        var id: String { rawValue }
    }

    @State private var mode = Mode.days
    @State private var days: Set<Day> = []
    @State private var at = ScheduleEditor.time("09:00")
    @State private var custom = ""
    /// The edited schedule as the core reads it; nil until something changes.
    @State private var draft: ScheduleInfo?
    @State private var draftError: String?
    @State private var asked = 0

    /// A day of the week as the core names it; `allCases` is cron's order
    /// (Sunday first), which the calendar's weekday symbols line up with.
    typealias Day = Components.Schemas.Picker.daysPayloadPayload

    private var shown: ScheduleInfo? { draft ?? current }
    private var changed: Bool { draft != nil && draft?.cron != setting.valueText }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            CardTitle(title: "Schedule", systemImage: "calendar") {
                if !setting.isDefault {
                    Button("Reset", action: reset)
                        .buttonStyle(.link)
                        .font(.subheadline)
                        .help(
                            "Back to the routine's own schedule: \(setting.defaultText.isEmpty ? "none" : setting.defaultText)"
                        )
                }
            }
            Divider().padding(.leading, SettingsMetrics.rowInset)
            summary
            Divider().padding(.leading, SettingsMetrics.rowInset)
            editor
        }
        .onAppear(perform: startFromCurrent)
        .onChange(of: current?.cron) { _, _ in startFromCurrent() }
    }

    /// The schedule in words, and when it next runs.
    private var summary: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Text(shown?.text ?? "No schedule").font(.title3.weight(.medium))
                if changed { Tag(text: "Not saved", tint: .blue) }
            }
            if let next = shown?.next, !next.isEmpty {
                Text("Next: " + next.map(Formats.slot).joined(separator: " · "))
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            } else if shown == nil {
                Text(setting.description)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.horizontal, SettingsMetrics.rowInset)
        .padding(.vertical, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var editor: some View {
        VStack(alignment: .leading, spacing: 12) {
            Picker("Edit as", selection: $mode) {
                ForEach(Mode.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            .onChange(of: mode) { _, new in
                // Custom starts from the schedule as it stands; days and time from
                // what the core reads in it.
                if new == .custom, custom.isEmpty { custom = shown?.cron ?? "" }
                if new == .days, let picker = shown?.picker { fill(picker) }
            }
            switch mode {
            case .days: daysAndTime
            case .custom: cron
            }
            if let message = draftError ?? error { RowError(message: message) }
            HStack {
                Spacer()
                Button("Revert", action: startFromCurrent).disabled(!changed)
                Button("Save") { if let cron = draft?.cron { save(cron) } }
                    .keyboardShortcut(.defaultAction)
                    .disabled(!changed)
            }
        }
        .padding(.horizontal, SettingsMetrics.rowInset)
        .padding(.vertical, 12)
    }

    private var daysAndTime: some View {
        HStack(spacing: 14) {
            HStack(spacing: 5) {
                ForEach(Self.orderedDays, id: \.self) { index in
                    DayToggle(
                        label: Calendar.current.veryShortWeekdaySymbols[index],
                        help: Calendar.current.weekdaySymbols[index],
                        on: days.contains(Day.allCases[index])
                    ) {
                        let day = Day.allCases[index]
                        if days.remove(day) == nil { days.insert(day) }
                        build()
                    }
                }
            }
            Text("at").foregroundStyle(.secondary)
            DatePicker("Time", selection: $at, displayedComponents: .hourAndMinute)
                .labelsHidden()
                .datePickerStyle(.stepperField)
                .onChange(of: at) { _, _ in build() }
        }
    }

    private var cron: some View {
        VStack(alignment: .leading, spacing: 4) {
            TextField("Cron", text: $custom, prompt: Text("minute hour day month weekday"))
                .labelsHidden()
                .textFieldStyle(.roundedBorder)
                .font(.body.monospaced())
                .frame(maxWidth: 260)
                .onSubmit(describeCustom)
            Text("Press Return to see what it means, then Save.")
                .font(.subheadline)
                .foregroundStyle(.secondary)
        }
    }

    /// Indices into the day names, starting on the calendar's first weekday.
    private static var orderedDays: [Int] {
        let first = Calendar.current.firstWeekday - 1
        return (0..<7).map { (first + $0) % 7 }
    }

    private func startFromCurrent() {
        draft = nil
        draftError = nil
        custom = current?.cron ?? setting.valueText
        if let picker = current?.picker {
            mode = .days
            fill(picker)
        } else {
            mode = setting.valueText.isEmpty ? .days : .custom
            days = []
        }
    }

    private func fill(_ picker: Components.Schemas.Picker) {
        days = Set(picker.days)
        at = Self.time(picker.at)
    }

    /// Asks the core for the schedule of the chosen days and time.
    private func build() {
        let chosen = Day.allCases.filter(days.contains)
        let time = Self.text(at)
        ask { $0.buildSchedule(days: chosen, at: time) }
    }

    private func describeCustom() {
        let text = custom
        ask { $0.describeSchedule(text) }
    }

    private func ask(_ work: @escaping @Sendable (CLI) -> Result<ScheduleInfo, CLI.Failure>) {
        asked += 1
        let mine = asked
        Task {
            let result = await cli.background(work)
            guard mine == asked else { return }
            switch result {
            case .success(let info):
                draft = info
                draftError = nil
            case .failure(let failure):
                draft = nil
                draftError = failure.message
            }
        }
    }

    /// The picker's "HH:MM" as a time for the time field, and back.
    private static func time(_ text: String) -> Date {
        formatter.date(from: text) ?? Date()
    }

    private static func text(_ date: Date) -> String { formatter.string(from: date) }

    private static let formatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "HH:mm"
        return formatter
    }()
}

/// One day of the week in the picker: a round button, filled when chosen.
struct DayToggle: View {
    let label: String
    let help: String
    let on: Bool
    let toggle: () -> Void

    var body: some View {
        Button(action: toggle) {
            Text(label)
                .font(.callout.weight(.semibold))
                .frame(width: 28, height: 28)
                .foregroundStyle(on ? Color.white : Color.primary)
                .background(on ? Color.accentColor : Color.primary.opacity(0.07), in: Circle())
                .contentShape(Circle())
        }
        .buttonStyle(.plain)
        .help(help)
        .accessibilityLabel(help)
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}
