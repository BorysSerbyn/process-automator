import Foundation

/// The app this build is, as its Info.plist names it (scripts/bundle.sh
/// writes it from the build's install.env): one automator-ui serves every app
/// built on the core.
public enum AppInfo {
    /// What people see: the app, its windows and its menu.
    public static var name: String { string("CFBundleName") ?? "Process Automator" }

    /// The core's CLI, bundled next to automator-ui.
    public static var command: String { string("ProcAutoCommand") ?? "procauto" }

    /// The build's version.
    public static var version: String { string("ProcAutoVersion") ?? "dev" }

    private static func string(_ key: String) -> String? {
        guard let value = Bundle.main.object(forInfoDictionaryKey: key) as? String, !value.isEmpty else { return nil }
        return value
    }
}
