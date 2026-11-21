import Foundation

/// Follows the core's run status through its CLI (`status --watch`), which
/// prints it as one JSON line at once and again each time it changes. If the
/// command ends (an update replaced the core, it crashed), it's started again
/// after a pause; meanwhile the last status stands, and goes red once past its
/// `validUntil`, which the caller checks on a timer.
public final class StatusStream: @unchecked Sendable {
    private let cli: CLI
    private let onStatus: @Sendable (RunStatus?) -> Void
    /// Everything below is only touched on `queue`.
    private let queue = DispatchQueue(label: "status-stream")
    private var process: Process?
    private var buffer = Data()
    private var stopped = false

    /// onStatus gets each status (nil: one the app can't read), on a
    /// background queue.
    public init(cli: CLI, onStatus: @escaping @Sendable (RunStatus?) -> Void) {
        self.cli = cli
        self.onStatus = onStatus
    }

    public func start() { queue.async { self.launch() } }

    public func stop() {
        queue.async {
            self.stopped = true
            self.process?.terminate()
        }
    }

    private func launch() {
        guard !stopped else { return }
        buffer = Data()
        let process = Process()
        process.executableURL = cli.executable
        process.arguments = ["status", "--watch"]
        let out = Pipe()
        process.standardOutput = out
        process.standardError = FileHandle.nullDevice
        out.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard let self else { return }
            self.queue.async { self.consume(data) }
        }
        process.terminationHandler = { [weak self] _ in
            out.fileHandleForReading.readabilityHandler = nil
            guard let self else { return }
            self.queue.asyncAfter(deadline: .now() + 5) { self.launch() }
        }
        do {
            try process.run()
            self.process = process
        } catch {
            out.fileHandleForReading.readabilityHandler = nil
            onStatus(nil)
            queue.asyncAfter(deadline: .now() + 5) { [weak self] in self?.launch() }
        }
    }

    private func consume(_ data: Data) {
        guard !data.isEmpty else { return }  // end of output: termination restarts it
        buffer.append(data)
        for line in Self.lines(from: &buffer) {
            onStatus(Self.status(from: line))
        }
    }

    /// Takes the complete lines out of buffer, leaving a partial last one.
    public static func lines(from buffer: inout Data) -> [Data] {
        var out: [Data] = []
        while let newline = buffer.firstIndex(of: 0x0A) {
            let line = buffer[buffer.startIndex..<newline]
            if !line.isEmpty { out.append(Data(line)) }
            buffer.removeSubrange(buffer.startIndex...newline)
        }
        return out
    }

    /// One line's status; unreadable or of an unsupported version → nil.
    public static func status(from line: Data) -> RunStatus? {
        guard let status = try? RunStatus.decode(line), status.schemaVersion == RunStatus.supportedSchemaVersion
        else { return nil }
        return status
    }
}
