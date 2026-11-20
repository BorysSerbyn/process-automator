import AutomatorUICore
import SwiftUI

/// The routine's own memories: what its runs are given to remember. Only the
/// user adds and removes them here; runs only read them.
struct RoutineMemoriesView: View {
    @ObservedObject var model: RoutineModel
    @State private var text = ""
    @State private var adding = false

    var body: some View {
        SettingsCard {
            CardTitle(title: "Memories", systemImage: "brain") {
                if let memories = model.memories, !memories.isEmpty {
                    Text(memories.count == 1 ? "1 memory" : "\(memories.count) memories")
                        .font(.subheadline)
                        .foregroundStyle(.tertiary)
                }
            }
            Text("What \(model.name)'s runs are given to remember, besides the ones every routine gets.")
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.horizontal, SettingsMetrics.rowInset)
                .padding(.bottom, 10)
            if let memories = model.memories {
                if memories.isEmpty {
                    Divider().padding(.leading, SettingsMetrics.rowInset)
                    Text("No memories yet.")
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity)
                        .padding(18)
                }
                ForEach(memories, id: \.id) { memory in
                    Divider().padding(.leading, SettingsMetrics.rowInset)
                    MemoryRow(memory: memory) { model.removeMemory(memory.id) }
                }
            }
            Divider().padding(.leading, SettingsMetrics.rowInset)
            addRow
            if let error = model.memoryError {
                RowError(message: error)
                    .padding(.horizontal, SettingsMetrics.rowInset)
                    .padding(.bottom, 10)
            }
        }
    }

    private var addRow: some View {
        HStack(alignment: .center, spacing: 10) {
            TextField("Memory", text: $text, prompt: Text("Something \(model.name) should remember"), axis: .vertical)
                .labelsHidden()
                .textFieldStyle(.roundedBorder)
                .lineLimit(1...4)
                .onSubmit(add)
            Button("Add", action: add)
                .disabled(adding || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }
        .padding(.horizontal, SettingsMetrics.rowInset)
        .padding(.vertical, 12)
    }

    private func add() {
        let memory = text
        guard !memory.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        adding = true
        Task {
            if await model.addMemory(memory) { text = "" }
            adding = false
        }
    }
}

/// One memory, with a button to forget it.
struct MemoryRow: View {
    let memory: Memory
    let remove: () -> Void
    @State private var hovering = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(memory.text)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
            Button(action: remove) {
                Image(systemName: "trash")
            }
            .buttonStyle(.borderless)
            .foregroundStyle(hovering ? Color.red : Color.secondary)
            .help("Forget this memory")
        }
        .padding(.horizontal, SettingsMetrics.rowInset)
        .padding(.vertical, 10)
        .onHover { hovering = $0 }
    }
}
