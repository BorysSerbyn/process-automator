// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "AutomatorUI",
    platforms: [.macOS(.v14)],
    dependencies: [
        // Generates the core's types from contracts/openapi.yaml at build time.
        .package(url: "https://github.com/apple/swift-openapi-generator", from: "1.14.0"),
        .package(url: "https://github.com/apple/swift-openapi-runtime", from: "1.13.0"),
    ],
    targets: [
        // Talking to the core (CLI, run status, notifications), testable without AppKit.
        .target(
            name: "AutomatorUICore",
            dependencies: [.product(name: "OpenAPIRuntime", package: "swift-openapi-runtime")],
            plugins: [.plugin(name: "OpenAPIGenerator", package: "swift-openapi-generator")]
        ),
        // The app: the indicator (menu bar icon and menu), the settings window, ….
        .executableTarget(name: "AutomatorUI", dependencies: ["AutomatorUICore"]),
        .testTarget(name: "AutomatorUICoreTests", dependencies: ["AutomatorUICore"]),
    ]
)
