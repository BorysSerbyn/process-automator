import Foundation

/// The notifications file (contracts/notifications.schema.json), generated from
/// the Go core's types. The core records notifications; automator-ui shows the
/// ones not delivered yet, marks them delivered, and on a click asks the core
/// to do the click action (`notifications open <id>`).
public typealias NotificationsFile = Components.Schemas.NotificationsFile
public typealias CoreNotification = Components.Schemas.Notification

extension NotificationsFile {
    public static let supportedSchemaVersion = 1

    public static func decode(_ data: Data) throws -> NotificationsFile {
        try CoreJSON.decoder().decode(NotificationsFile.self, from: data)
    }

    /// Loads the file. Missing, unreadable or unsupported → nil.
    public static func load(from url: URL) -> NotificationsFile? {
        guard let data = try? Data(contentsOf: url),
            let file = try? decode(data),
            file.schemaVersion == supportedSchemaVersion
        else { return nil }
        return file
    }

    /// The ones no frontend has delivered yet, oldest first.
    public var pending: [CoreNotification] { notifications.filter { $0.deliveredAt == nil } }
}
