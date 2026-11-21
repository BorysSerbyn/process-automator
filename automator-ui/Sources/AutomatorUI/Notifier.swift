import AppKit
import AutomatorUICore
import UserNotifications

/// Desktop notifications for the app's own notifications the core records
/// (runs that failed, updates): shows the ones no frontend delivered yet,
/// tells the core which were delivered, and has the core carry out a click.
/// What routines send (the notify plugin) the core shows itself.
@MainActor
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    private var cli: CLI?
    /// Notifications already handed to macOS, until the core marks them delivered.
    private var posted: Set<String> = []

    /// Notifications need the app bundle (not `swift run`).
    private var canNotify: Bool { Bundle.main.bundleIdentifier != nil }

    func setUp(cli: CLI?) {
        self.cli = cli
        guard canNotify else { return }
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        center.requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }

    /// Shows the pending notifications in the core's file. While macOS doesn't
    /// allow notifications (not answered yet, or turned off), they stay pending.
    func deliver(from url: URL) {
        guard canNotify, let cli, let file = NotificationsFile.load(from: url) else { return }
        let fresh = file.pending.filter { !posted.contains($0.id) }
        guard !fresh.isEmpty else { return }
        posted.formUnion(fresh.map(\.id))  // claimed: the next reload won't post them again
        Task {
            let center = UNUserNotificationCenter.current()
            let allowed = await center.notificationSettings().authorizationStatus
            guard allowed == .authorized || allowed == .provisional else {
                posted.subtract(fresh.map(\.id))
                return
            }
            var delivered: [String] = []
            for note in fresh {
                let content = UNMutableNotificationContent()
                content.title = note.title
                content.body = note.body ?? ""
                content.sound = .default
                content.userInfo = ["id": note.id]
                do {
                    try await center.add(UNNotificationRequest(identifier: note.id, content: content, trigger: nil))
                    delivered.append(note.id)
                } catch {
                    posted.remove(note.id)
                }
            }
            guard !delivered.isEmpty else { return }
            let ids = delivered
            await cli.background { $0.markDelivered(ids) }
        }
    }

    /// A click: the core carries out the notification's click action.
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse
    ) async {
        guard response.actionIdentifier == UNNotificationDefaultActionIdentifier,
            let id = response.notification.request.content.userInfo["id"] as? String,
            let cli = CLI.bundled
        else { return }
        await cli.background { $0.openNotification(id) }
    }

    /// Shown as banners even if the app happens to be active.
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter, willPresent notification: UNNotification
    ) async -> UNNotificationPresentationOptions {
        [.banner, .sound, .list]
    }
}
