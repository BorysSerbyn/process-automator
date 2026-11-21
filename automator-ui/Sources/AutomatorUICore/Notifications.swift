import Foundation

/// One of the app's own notifications (a run that failed, an update), as the
/// core lists them (`notifications list --pending --json`; generated from
/// the Go core's types). automator-ui shows the pending ones, marks them
/// delivered, and on a click asks the core to show the run
/// (`notifications open <id>`). It reads no file: only the CLI.
public typealias CoreNotification = Components.Schemas.Notification
