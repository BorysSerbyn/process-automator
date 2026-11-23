import AppKit
import AutomatorUICore
import UserNotifications

/// Desktop notifications for the app's own notifications (runs that failed,
/// updates): asks the core for the ones no frontend delivered yet, shows
/// them, and tells the core which were delivered. A click opens the app
/// where the core says (`show`).
@MainActor
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    private var cli: CLI?
    /// Shows what a click on one should: its run, or the app.
    private var show: (Components.Schemas.ClickOutput) -> Void = { _ in }
    /// Notifications already handed to macOS, until the core marks them delivered.
    private var posted: Set<String> = []
    /// Whether the core is being asked for pending ones (one ask at a time).
    private var asking = false

    nonisolated static let acknowledgeCategory = "acknowledge"
    nonisolated static let acknowledgeAction = "acknowledge"

    /// Notifications need the app bundle (not `swift run`).
    private var canNotify: Bool { Bundle.main.bundleIdentifier != nil }

    func setUp(cli: CLI?, show: @escaping (Components.Schemas.ClickOutput) -> Void) {
        self.cli = cli
        self.show = show
        guard canNotify else { return }
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        // A routine's or hook's notification that waits for me has an
        // Acknowledge button.
        let acknowledge = UNNotificationAction(identifier: Self.acknowledgeAction, title: "Acknowledge", options: [])
        center.setNotificationCategories([
            UNNotificationCategory(identifier: Self.acknowledgeCategory, actions: [acknowledge], intentIdentifiers: [])
        ])
        center.requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }

    /// Shows the notifications the core says are pending. While macOS doesn't
    /// allow notifications (not answered yet, or turned off), they stay pending.
    func deliver() {
        guard canNotify, let cli, !asking else { return }
        asking = true
        Task {
            let pending = (try? await cli.background { $0.pendingNotifications() }.get()) ?? []
            asking = false
            post(pending.filter { !posted.contains($0.id) }, cli: cli)
        }
    }

    private func post(_ fresh: [CoreNotification], cli: CLI) {
        guard !fresh.isEmpty else { return }
        posted.formUnion(fresh.map(\.id))  // claimed: the next delivery won't post them again
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
                if note.needsAcknowledging { content.categoryIdentifier = Self.acknowledgeCategory }
                if note.urgent == true { content.interruptionLevel = .timeSensitive }
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

    /// A click opens the app: the core says on what.
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse
    ) async {
        guard let id = response.notification.request.content.userInfo["id"] as? String, let cli = CLI.bundled
        else { return }
        // Acknowledge, on one that waits for me: the core stops waiting.
        if response.actionIdentifier == Self.acknowledgeAction {
            if case .failure(let failure) = await cli.background({ $0.acknowledge([id]) }) {
                await MainActor.run { showAlert("Couldn't acknowledge it", failure.message) }
            }
            return
        }
        guard response.actionIdentifier == UNNotificationDefaultActionIdentifier else { return }
        let result = await cli.background { $0.openNotification(id) }
        await MainActor.run {
            switch result {
            case .success(let click): show(click)
            case .failure(let failure): showAlert("Couldn't open the notification", failure.message)
            }
        }
    }

    /// Shown as banners even if the app happens to be active.
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter, willPresent notification: UNNotification
    ) async -> UNNotificationPresentationOptions {
        [.banner, .sound, .list]
    }
}
