import Foundation
import Testing

@testable import AutomatorUICore

// Opened from outside the window (the menu, a notification, opening the
// app): the section it belongs to, with only it opened there.
@Test func openingFromOutsideShowsItInItsSection() {
    var nav = NavigationState(section: .settings)
    nav.show(.routine("standup"))
    #expect(nav.section == .routines && nav.path == [.routine("standup")])
    #expect(nav.shown == .routine("standup") && nav.canGoBack)

    nav.show(.watcher("cal"))
    #expect(nav.section == .hooks && nav.path == [.watcher("cal")])
    nav.show(.hook("open-meet"))
    #expect(nav.section == .hooks && nav.path == [.hook("open-meet")])

    for section in SidebarSection.allCases {
        nav.show(section.root)
        #expect(nav.section == section && nav.path.isEmpty && !nav.canGoBack)
    }
}

// Back from what was opened from a list is the list.
@Test func backGoesToWhatItWasOpenedFrom() {
    var nav = NavigationState()
    nav.show(.routine("standup"))
    #expect(nav.backTitle == "Routines")
    nav.push(.routine("digest"))
    #expect(nav.backTitle == "standup")
    nav.back()
    nav.back()
    #expect(nav.section == .routines && nav.shown == .routines)
    nav.back()  // nothing to go back to
    #expect(nav.shown == .routines && nav.backTitle == nil)
}

// Opened from inside the window: on top of what shows, in the same
// section; one already open is gone back to, so a hook and its watcher
// opening each other don't pile up.
@Test func openingFromInsideStaysInTheSection() {
    var nav = NavigationState(section: .setup)
    nav.push(.routine("standup"))
    #expect(nav.section == .setup && nav.path == [.routine("standup")])

    nav.show(.hook("open-meet"))
    nav.push(.watcher("cal"))
    nav.push(.hook("other"))
    #expect(nav.path == [.hook("open-meet"), .watcher("cal"), .hook("other")])
    nav.push(.watcher("cal"))
    #expect(nav.path == [.hook("open-meet"), .watcher("cal")])
    nav.push(.hook("open-meet"))
    #expect(nav.path == [.hook("open-meet")])

    // A section's own screen (Settings, say) switches to it.
    nav.push(.settings)
    #expect(nav.section == .settings && nav.path.isEmpty)
}

@Test func choosingASectionShowsItsOwnScreen() {
    var nav = NavigationState()
    nav.show(.routine("standup"))
    nav.select(.acknowledgements)
    #expect(nav.section == .acknowledgements && nav.path.isEmpty && nav.open == [.acknowledgements])
}

// Deleted: it closes, and what was opened from it.
@Test func whatsDeletedCloses() {
    var nav = NavigationState()
    nav.show(.hook("open-meet"))
    nav.push(.watcher("cal"))
    nav.removed(.hook("open-meet"))
    #expect(nav.section == .hooks && nav.path.isEmpty)
    nav.removed(.routine("gone"))  // not open: nothing changes
    #expect(nav.shown == .hooks)
}

@Test func destinationsAreNamedAsTheSidebarAndTheirItems() {
    #expect(
        SidebarSection.allCases.map(\.title) == [
            "Routines", "Hooks", "Acknowledgements", "Setup", "Settings", "Updates",
        ])
    #expect(SidebarSection.groups.flatMap { $0 } == SidebarSection.allCases)
    #expect(Destination.routine("standup").title == "standup")
    #expect(Destination.updates.title == "Updates" && Destination.updates.isRoot)
    #expect(!Destination.watcher("cal").isRoot)
}

// The sidebar says how many notifications wait and how many setup steps are left.
@Test func theSidebarCountsWhatWaits() throws {
    let status = try RunStatus.decode(
        Data(
            """
            {"schemaVersion":1,"updatedAt":"2026-11-18T09:30:00Z","health":"error","busy":false,"routines":[],
             "setupLeft":["backend","turn-on"],
             "waiting":[{"id":"a","title":"Hi"}]}
            """.utf8))
    #expect(SidebarSection.acknowledgements.badge(for: status) == "1")
    #expect(SidebarSection.setup.badge(for: status) == "2 to do")
    #expect(SidebarSection.routines.badge(for: status) == nil)
    #expect(SidebarSection.setup.badge(for: nil) == nil)
}
