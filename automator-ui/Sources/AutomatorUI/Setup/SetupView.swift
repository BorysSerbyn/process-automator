import AutomatorUICore
import SwiftUI

/// The Setup window: setup's steps grouped by kind, in the core's order
/// (templates, model backend, plan usage, Claude Code, routines to fix,
/// sign-ins, routines to turn on), like the settings' groups. A group is open while any of its steps isn't
/// done; each step shows where it stands and what does it.
struct SetupView: View {
    @ObservedObject var model: SetupViewModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                header
                if let error = model.loadError {
                    Banner(
                        title: "Couldn't read setup", message: error, systemImage: "xmark.octagon.fill", tint: .red)
                }
                ForEach(groups, id: \.kind) { group in
                    SettingsCard {
                        if group.single {
                            SetupStepView(model: model, step: group.steps[0], prominent: true)
                        } else {
                            SetupGroupView(model: model, group: group)
                        }
                    }
                }
            }
            .padding(20)
            .frame(maxWidth: 760)
            .frame(maxWidth: .infinity)
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 560, idealWidth: 640, minHeight: 420, idealHeight: 680)
    }

    /// The steps by kind, in the order the core lists them.
    private var groups: [SetupGroup] {
        var groups: [SetupGroup] = []
        for step in model.steps {
            if groups.last?.kind == step.step {
                groups[groups.count - 1].steps.append(step)
            } else {
                groups.append(SetupGroup(kind: step.step, steps: [step]))
            }
        }
        return groups
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 12) {
            Image(systemName: model.done ? "checkmark.seal.fill" : "checklist")
                .font(.system(size: 28))
                .foregroundStyle(model.done ? Color.green : Color.accentColor)
                .frame(width: 36)
            VStack(alignment: .leading, spacing: 2) {
                Text("Setup").font(.title2.weight(.semibold))
                if model.loaded {
                    Text(subtitle).font(.subheadline).foregroundStyle(.secondary)
                }
            }
            Spacer()
            Button(action: model.load) {
                Label("Check Again", systemImage: "arrow.clockwise")
            }
            .help("Read where setup stands again")
        }
    }

    private var subtitle: String {
        if model.done { return "Everything is set up." }
        return model.remaining == 1 ? "1 step to go." : "\(model.remaining) steps to go."
    }
}

/// The steps of one kind.
struct SetupGroup {
    typealias Kind = Components.Schemas.SetupStep.stepPayload
    let kind: Kind
    var steps: [SetupStep]

    var id: String { "group:" + kind.rawValue }
    var allDone: Bool { steps.allSatisfy(\.done) }

    /// Templates, the model backend, plan usage and Claude Code are one step
    /// each: a row of their own, not a group of one.
    var single: Bool { steps.count == 1 && [.templates, .backend, .usage, .mcp].contains(kind) }

    /// The UI's names for the core's kinds of step.
    var title: String {
        switch kind {
        case .templates: "Templates"
        case .backend: "Model backend"
        case .usage: "Plan usage"
        case .mcp: "Claude Code"
        case .routine: "Routines to fix"
        case .sign_hyphen_in: "Sign-ins"
        case .turn_hyphen_on: "Turn on routines"
        case .app: "Apps to keep running"
        }
    }

    /// How far along it is: "4 of 5 signed in".
    var progress: String {
        let done = steps.filter(\.done).count
        let of = "\(done) of \(steps.count)"
        switch kind {
        case .routine:
            let left = steps.count - done
            return left == 0 ? "All fixed" : left == 1 ? "1 to fix" : "\(left) to fix"
        case .sign_hyphen_in: return "\(of) signed in"
        case .turn_hyphen_on: return "\(of) on"
        case .app: return "\(of) running"
        case .templates, .backend, .usage, .mcp: return "\(of) done"
        }
    }
}

/// A group of steps of one kind: a header showing how far along it is (the
/// whole row opens and closes it), then a row per step.
struct SetupGroupView: View {
    @ObservedObject var model: SetupViewModel
    let group: SetupGroup

    private var open: Bool { model.isOpen(group) }

    private func toggle() { model.toggle(group) }

    var body: some View {
        DisclosureRow(open: open, help: group.title, toggle: toggle) {
            Text(group.title).font(.title3.weight(.semibold))
            Spacer(minLength: 8)
            if group.allDone {
                Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
            }
            Text(group.progress)
                .font(.subheadline)
                .foregroundStyle(group.allDone ? Color.secondary : Color.primary)
        }
        if open {
            ForEach(group.steps, id: \.subject) { step in
                Divider().padding(.leading, SettingsMetrics.rowInset)
                if step.step == .turn_hyphen_on {
                    SetupStepView(model: model, step: step, prominent: false)
                } else {
                    SetupStepRow(model: model, step: step)
                }
            }
        }
    }
}

/// A step that opens to what it needs: the templates to pick, the backend's
/// choice, a routine's review before turning it on. Steps not done start open.
struct SetupStepView: View {
    @ObservedObject var model: SetupViewModel
    let step: SetupStep
    /// A step of its own (templates, backend), or a row in a group.
    let prominent: Bool

    private var id: String { SetupViewModel.id(step) }
    private var open: Bool { model.isOpen(step) }

    private func toggle() { model.toggle(step) }

    var body: some View {
        DisclosureRow(open: open, prominent: prominent, help: step.title, toggle: toggle) {
            StepMark(done: step.done, optional: step.optional == true)
            StepText(step: step, prominent: prominent, expanded: open)
            StepTags(model: model, step: step)
        }
        if open {
            VStack(alignment: .leading, spacing: 12) {
                // The question is for doing the step; a done one shows its controls only.
                if let ask = step.ask, !ask.isEmpty, !step.done {
                    Text(ask).fixedSize(horizontal: false, vertical: true)
                }
                action
                if step.signIn != nil { SignInButton(model: model, step: step) }
                if let error = model.errors[id] { RowError(message: error) }
            }
            .padding(.leading, 56)
            .padding(.trailing, SettingsMetrics.rowInset)
            .padding(.bottom, 14)
        }
    }

    @ViewBuilder private var action: some View {
        switch step.step {
        case .templates:
            TemplatePicker(model: model, step: step)
        case .backend:
            BackendPicker(model: model, step: step)
        case .turn_hyphen_on:
            // What it can reach, then one button: trusting it and running it
            // on its schedule (the question above says which it needs).
            // The same review as a window's Turn On…, then its one confirm.
            if let routine = step.subject {
                TurnOnReview(
                    summary: nil, review: model.reviews[routine], loadError: model.reviewErrors[routine],
                    retry: { model.review(routine, step) }, signingIn: model.signingIn,
                    signInError: model.signInError, signIn: model.signIn(named:), granter: model.granter)
                if !step.done, let review = model.reviews[routine] {
                    HStack {
                        if model.working.contains(id) { ProgressView().controlSize(.small) }
                        Spacer()
                        Button(review.confirmTitle) { model.turnOn(step) }
                            .buttonStyle(.borderedProminent)
                            .disabled(model.working.contains(id))
                    }
                }
            }
        case .usage:
            // The core wraps the user's Claude Code status line to record usage.
            if !step.done {
                HStack(spacing: 8) {
                    Button("Set Up") { model.setUpUsage(step) }.disabled(model.working.contains(id))
                    if model.working.contains(id) { ProgressView().controlSize(.small) }
                }
            }
        case .mcp:
            // Done without asking when the window shows it; the button is for
            // trying again after a failure.
            if !step.done {
                HStack(spacing: 8) {
                    Button("Connect") { model.connectClaudeCode(step) }.disabled(model.working.contains(id))
                    if model.working.contains(id) { ProgressView().controlSize(.small) }
                }
            }
        case .routine, .sign_hyphen_in, .app:
            EmptyView()  // rows of their own (SetupStepRow)
        }
    }
}

/// A step done with one button, right on its row: a sign-in, a routine to
/// fix (its window shows what's wrong).
struct SetupStepRow: View {
    @ObservedObject var model: SetupViewModel
    let step: SetupStep

    private var id: String { SetupViewModel.id(step) }

    /// What to do outside the app first (a sign-in needing, e.g., a Slack app).
    private var prepare: String? {
        guard !step.done, let text = step.prepare, !text.isEmpty else { return nil }
        return text
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                StepMark(done: step.done, optional: step.optional == true)
                StepText(step: step, prominent: false, expanded: true)
                if !step.done, prepare == nil { button }
                StepTags(model: model, step: step)
            }
            if let prepare {
                PreparationView(model: model, step: step, text: prepare)
                    .padding(.leading, 28)
                    .padding(.top, 4)
            }
            if let error = model.errors[id] {
                RowError(message: error).padding(.leading, 28)
            }
        }
        .padding(.leading, SettingsMetrics.rowInset + 20)
        .padding(.trailing, SettingsMetrics.rowInset)
        .padding(.vertical, 8)
    }

    @ViewBuilder private var button: some View {
        switch step.step {
        case .sign_hyphen_in:
            if step.signIn != nil { SignInButton(model: model, step: step) }
        case .routine:
            if let routine = step.subject {
                Button("Open \(routine)…") { model.openRoutine(routine) }
            }
        case .app:
            Button("Open") { model.openApp(step) }
                .disabled(model.working.contains(id))
                .help("Open the app in the background")
        case .templates, .backend, .turn_hyphen_on, .usage, .mcp:
            EmptyView()
        }
    }
}

/// A step's title and the core's status under it.
struct StepText: View {
    let step: SetupStep
    let prominent: Bool
    let expanded: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(step.title).font(prominent ? .title3.weight(.semibold) : .headline)
            Text(step.status)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .lineLimit(expanded ? nil : 1)
                .fixedSize(horizontal: false, vertical: expanded)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// Done, Optional, or a spinner while the core works on it.
struct StepTags: View {
    @ObservedObject var model: SetupViewModel
    let step: SetupStep

    var body: some View {
        if model.working.contains(SetupViewModel.id(step)) {
            ProgressView().controlSize(.small)
        } else if step.done {
            Tag(text: "Done", tint: .green)
        } else if step.optional == true {
            Tag(text: "Optional")
        }
    }
}

/// Sign In… (or Enter API Key… for the backend), where the step's signIn
/// says; it waits for the browser.
struct SignInButton: View {
    @ObservedObject var model: SetupViewModel
    let step: SetupStep

    var body: some View {
        let busy = model.working.contains(SetupViewModel.id(step))
        HStack(spacing: 10) {
            if busy {
                Text("Finish in your browser.").font(.subheadline).foregroundStyle(.secondary)
            }
            Button(step.step == .backend ? "Enter API Key…" : "Sign In…") { model.signIn(step) }
                .disabled(busy)
        }
    }
}

/// A step's mark: a green check when done, else a ring (dashed and grey
/// when the step is optional).
struct StepMark: View {
    let done: Bool
    var optional = false

    var body: some View {
        ZStack {
            if done {
                Image(systemName: "checkmark.circle.fill")
                    .font(.system(size: 20))
                    .foregroundStyle(.green)
            } else {
                Circle()
                    .strokeBorder(
                        optional ? Color.secondary : Color.accentColor,
                        style: StrokeStyle(lineWidth: 1.5, dash: optional ? [3, 2.5] : []))
            }
        }
        .frame(width: 20, height: 20)
        .accessibilityLabel(done ? "Done" : optional ? "Optional, not done" : "Not done")
    }
}

/// The templates to install as routines: none chosen at first; a chosen one
/// shows its parameters (blank ones take the template's default).
struct TemplatePicker: View {
    @ObservedObject var model: SetupViewModel
    let step: SetupStep

    var body: some View {
        if model.templates != nil {
            if !model.allProjects.isEmpty { ProjectPicker(model: model) }
            let offered = model.offered
            if !offered.isEmpty {
                SettingsCard(nested: true) {
                    ForEach(Array(offered.enumerated()), id: \.element.id) { index, template in
                        if index > 0 { Divider().padding(.leading, 12) }
                        TemplateRow(model: model, template: template)
                    }
                }
            }
            if model.templates?.isEmpty == true {
                Text("No templates to install.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            } else {
                installButton
            }
        } else if model.errors[SetupViewModel.id(step)] == nil {
            ProgressView().controlSize(.small)
        }
    }

    private var installButton: some View {
        HStack {
            Spacer()
            Button("Install Selected") { model.install(step) }
                .keyboardShortcut(.defaultAction)
                .disabled(
                    model.offered.allSatisfy { !model.chosen.contains($0.id) }
                        || model.working.contains(SetupViewModel.id(step)))
        }
    }
}

/// Which projects the user works on, as chips (none picked at first):
/// the templates offered follow.
struct ProjectPicker: View {
    @ObservedObject var model: SetupViewModel

    var body: some View {
        // The step's question asks which projects; the chips answer it.
        VStack(alignment: .leading, spacing: 8) {
            FlowLayout(spacing: 6) {
                ForEach(model.allProjects, id: \.self) { project in
                    let on = model.projects.contains(project)
                    Button {
                        model.pickProject(project, !on)
                    } label: {
                        Label(project, systemImage: on ? "checkmark" : "plus")
                            .font(.callout.weight(.medium))
                            .padding(.horizontal, 10)
                            .padding(.vertical, 4)
                            .foregroundStyle(on ? Color.white : Color.primary)
                            .background(
                                on ? Color.accentColor : Color.primary.opacity(0.07), in: Capsule()
                            )
                            .contentShape(Capsule())
                    }
                    .buttonStyle(.plain)
                    .accessibilityAddTraits(on ? .isSelected : [])
                }
            }
            Text(
                model.projects.isEmpty
                    ? "Templates for any project are below; pick your projects to see theirs too."
                    : "Showing templates for any project and for the projects picked."
            )
            .font(.subheadline)
            .foregroundStyle(.secondary)
        }
    }
}

struct TemplateRow: View {
    @ObservedObject var model: SetupViewModel
    let template: TemplateInfo

    private var chosen: Bool { model.chosen.contains(template.id) }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Toggle(isOn: Binding(get: { chosen }, set: { model.choose(template.id, $0) })) {
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(template.name).font(.body.weight(.semibold))
                        Text(template.id).font(.caption.monospaced()).foregroundStyle(.tertiary)
                        if template.installed { Tag(text: "Installed") }
                    }
                    if let description = template.description, !description.isEmpty {
                        Text(description)
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    if let watcher = template.withWatcher, !watcher.installed {
                        Text("Comes with the watcher \(watcher.name) (\(watcher.id)), installed with it")
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            .toggleStyle(.checkbox)
            .disabled(template.installed)
            if chosen, let params = template.params, !params.isEmpty {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(params, id: \.name) { param in
                        ParamField(param: param, text: model.value(template.id, param.name))
                    }
                }
                .padding(.leading, 22)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
    }
}

/// A template parameter: its name (marked when required), a field showing
/// its default, its description.
struct ParamField: View {
    let param: Components.Schemas.Param
    @Binding var text: String

    private var placeholder: String { param.defaultText ?? (param.required ? "Required" : "") }

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 10) {
                HStack(spacing: 2) {
                    Text(param.name).font(.callout)
                    if param.required {
                        Text("*").font(.callout.weight(.bold)).foregroundStyle(.red).help("Required")
                    }
                }
                .frame(width: 120, alignment: .leading)
                if let choices = param._enum, !choices.isEmpty {
                    Picker(param.name, selection: $text) {
                        Text(placeholder.isEmpty ? "Default" : "Default (\(placeholder))").tag("")
                        ForEach(choices, id: \.self) { Text($0).tag($0) }
                    }
                    .labelsHidden()
                    .fixedSize()
                } else {
                    TextField(param.name, text: $text, prompt: Text(placeholder))
                        .labelsHidden()
                        .textFieldStyle(.roundedBorder)
                        .frame(maxWidth: 260)
                }
            }
            if let description = param.description, !description.isEmpty {
                Text(description)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.leading, 130)
            }
        }
    }
}

/// The model backend: the setting's choices as the core gives them (the
/// step's question says what each is; the API key, when one is missing, is
/// the step's sign-in).
struct BackendPicker: View {
    @ObservedObject var model: SetupViewModel
    let step: SetupStep

    var body: some View {
        if let backend = model.backend {
            let busy = model.working.contains(SetupViewModel.id(step))
            VStack(alignment: .leading, spacing: 8) {
                Picker(
                    "Backend",
                    selection: Binding(get: { backend.valueText }, set: { model.setBackend(step, $0) })
                ) {
                    ForEach(backend._enum ?? [], id: \.self) { Text($0).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .fixedSize()
                .disabled(busy)
                // What the chosen backend needs set (local: its server and
                // model), each with the Settings window's control.
                ForEach(model.backendSettings, id: \.key) { setting in
                    SettingRow(
                        setting: setting, label: setting.key, error: nil,
                        set: { model.setBackendSetting(step, setting.key, $0) },
                        reset: { model.setBackendSetting(step, setting.key, nil) }
                    )
                    .disabled(busy)
                }
            }
        } else if model.errors[SetupViewModel.id(step)] == nil {
            ProgressView().controlSize(.small)  // the error shows under the step otherwise
        }
    }
}

/// What to do outside the app before signing in (e.g. creating a Slack app),
/// as the core words it: its intro, then numbered steps (links open in the
/// browser). Sign In waits for "I've done this".
struct PreparationView: View {
    @ObservedObject var model: SetupViewModel
    let step: SetupStep
    let text: String

    private var id: String { SetupViewModel.id(step) }
    private var done: Bool { model.prepared.contains(id) }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label("First, set up \(step.subject ?? "this")", systemImage: "list.number")
                .font(.headline)
            VStack(alignment: .leading, spacing: 6) {
                ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        if let number = line.number {
                            Text(number)
                                .font(.callout.weight(.semibold).monospacedDigit())
                                .foregroundStyle(.secondary)
                                .frame(width: 18, alignment: .trailing)
                        }
                        Text(Self.rich(line.text))
                            .font(.callout)
                            .textSelection(.enabled)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            }
            HStack(spacing: 12) {
                Toggle(
                    "I've done this",
                    isOn: Binding(
                        get: { done },
                        set: { on in
                            if on { model.prepared.insert(id) } else { model.prepared.remove(id) }
                        })
                )
                .toggleStyle(.checkbox)
                Spacer()
                SignInButton(model: model, step: step).disabled(!done)
            }
        }
        .padding(12)
        .background(Color.accentColor.opacity(0.07), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Color.accentColor.opacity(0.2)))
    }

    /// The text's lines; "1. …" ones keep their number apart.
    private var lines: [(number: String?, text: String)] {
        text.split(separator: "\n", omittingEmptySubsequences: true).map { raw in
            let line = String(raw)
            if let dot = line.firstIndex(of: "."), line[..<dot].allSatisfy(\.isNumber), !line[..<dot].isEmpty {
                return (String(line[...dot]), line[line.index(after: dot)...].trimmingCharacters(in: .whitespaces))
            }
            return (nil, line)
        }
    }

    /// `code` shows as code, and web addresses (http, https) become links.
    private static func rich(_ text: String) -> AttributedString {
        var result =
            (try? AttributedString(
                markdown: text, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)))
            ?? AttributedString(text)
        let plain = String(result.characters)
        guard let detector = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue) else {
            return result
        }
        for match in detector.matches(in: plain, range: NSRange(plain.startIndex..., in: plain)) {
            guard let url = match.url, ["http", "https"].contains(url.scheme?.lowercased() ?? ""),
                let range = Range(match.range, in: plain),
                let start = AttributedString.Index(range.lowerBound, within: result),
                let end = AttributedString.Index(range.upperBound, within: result)
            else { continue }
            result[start..<end].link = url
        }
        return result
    }
}
