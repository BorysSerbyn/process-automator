//go:build darwin && cgo

package sysenv

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework IOKit -framework ApplicationServices -framework CoreGraphics -framework UserNotifications -framework EventKit -framework Contacts -framework AVFoundation
#import <Foundation/Foundation.h>
#import <IOKit/hidsystem/IOHIDLib.h>
#import <ApplicationServices/ApplicationServices.h>
#import <CoreGraphics/CoreGraphics.h>
#import <UserNotifications/UserNotifications.h>
#import <EventKit/EventKit.h>
#import <Contacts/Contacts.h>
#import <AVFoundation/AVFoundation.h>

// Which permission (pa_check, pa_request).
enum { PA_INPUT, PA_AX, PA_SCREEN, PA_NOTIFY, PA_CALENDARS, PA_REMINDERS, PA_CONTACTS, PA_MIC, PA_CAMERA };
// pa_check: granted, missing, or it can't be told.
enum { PA_GRANTED = 0, PA_MISSING = 1, PA_UNKNOWN = 2 };
// pa_request: asked; or why not: no way to ask, not inside the app's
// bundle, no usage description in its Info.plist.
enum { PA_ASKED = 1, PA_NO_API = 0, PA_NO_BUNDLE = -1, PA_NO_USAGE = -2 };

static BOOL pa_wait(dispatch_semaphore_t s, int secs) {
	return dispatch_semaphore_wait(s, dispatch_time(DISPATCH_TIME_NOW, (int64_t)secs * NSEC_PER_SEC)) == 0;
}

// pa_in_bundle: this executable is inside an app bundle. Notifications
// belong to a bundle: outside one, UserNotifications raises rather than
// failing, so it isn't called.
static BOOL pa_in_bundle(void) {
	NSBundle *app = [NSBundle mainBundle];
	return app.bundleIdentifier != nil && [app.bundleURL.pathExtension isEqualToString:@"app"];
}

// pa_usage: the app's Info.plist says why it asks for something (macOS ends
// a process that asks without saying).
static BOOL pa_usage(NSString *key) {
	return [[NSBundle mainBundle] objectForInfoDictionaryKey:key] != nil;
}

static int pa_check(int which) {
	@autoreleasepool {
		switch (which) {
		case PA_INPUT:
			// Unknown: never asked, so not allowed yet.
			return IOHIDCheckAccess(kIOHIDRequestTypeListenEvent) == kIOHIDAccessTypeGranted ? PA_GRANTED : PA_MISSING;
		case PA_AX:
			return AXIsProcessTrusted() ? PA_GRANTED : PA_MISSING;
		case PA_SCREEN:
			return CGPreflightScreenCaptureAccess() ? PA_GRANTED : PA_MISSING;
		case PA_NOTIFY: {
			if (!pa_in_bundle()) return PA_UNKNOWN;
			dispatch_semaphore_t s = dispatch_semaphore_create(0);
			__block UNAuthorizationStatus status = UNAuthorizationStatusNotDetermined;
			[[UNUserNotificationCenter currentNotificationCenter] getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *settings) {
				status = settings.authorizationStatus;
				dispatch_semaphore_signal(s);
			}];
			if (!pa_wait(s, 10)) return PA_UNKNOWN;
			return (status == UNAuthorizationStatusAuthorized || status == UNAuthorizationStatusProvisional) ? PA_GRANTED : PA_MISSING;
		}
		case PA_CALENDARS:
			return [EKEventStore authorizationStatusForEntityType:EKEntityTypeEvent] == EKAuthorizationStatusFullAccess ? PA_GRANTED : PA_MISSING;
		case PA_REMINDERS:
			return [EKEventStore authorizationStatusForEntityType:EKEntityTypeReminder] == EKAuthorizationStatusFullAccess ? PA_GRANTED : PA_MISSING;
		case PA_CONTACTS:
			return [CNContactStore authorizationStatusForEntityType:CNEntityTypeContacts] == CNAuthorizationStatusAuthorized ? PA_GRANTED : PA_MISSING;
		case PA_MIC:
			return [AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeAudio] == AVAuthorizationStatusAuthorized ? PA_GRANTED : PA_MISSING;
		case PA_CAMERA:
			return [AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeVideo] == AVAuthorizationStatusAuthorized ? PA_GRANTED : PA_MISSING;
		}
		return PA_UNKNOWN;
	}
}

// pa_can_request: whether macOS can be asked for a permission from this
// process (PA_ASKED), or why not.
static int pa_can_request(int which) {
	switch (which) {
	case PA_INPUT: case PA_AX: case PA_SCREEN:
		return PA_ASKED;
	case PA_NOTIFY:
		return pa_in_bundle() ? PA_ASKED : PA_NO_BUNDLE;
	case PA_CALENDARS:
		return pa_usage(@"NSCalendarsFullAccessUsageDescription") ? PA_ASKED : PA_NO_USAGE;
	case PA_REMINDERS:
		return pa_usage(@"NSRemindersFullAccessUsageDescription") ? PA_ASKED : PA_NO_USAGE;
	case PA_CONTACTS:
		return pa_usage(@"NSContactsUsageDescription") ? PA_ASKED : PA_NO_USAGE;
	case PA_MIC:
		return pa_usage(@"NSMicrophoneUsageDescription") ? PA_ASKED : PA_NO_USAGE;
	case PA_CAMERA:
		return pa_usage(@"NSCameraUsageDescription") ? PA_ASKED : PA_NO_USAGE;
	}
	return PA_NO_API;
}

// pa_request asks macOS for a permission (pa_can_request said it can): the
// first time, it shows its prompt, naming the app this process is
// attributed to. Those answered in a completion handler are waited for, up
// to two minutes.
static int pa_request(int which) {
	@autoreleasepool {
		dispatch_semaphore_t s = dispatch_semaphore_create(0);
		switch (which) {
		case PA_INPUT:
			IOHIDRequestAccess(kIOHIDRequestTypeListenEvent);
			return PA_ASKED;
		case PA_AX: {
			NSDictionary *opts = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt: @YES};
			AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)opts);
			return PA_ASKED;
		}
		case PA_SCREEN:
			CGRequestScreenCaptureAccess();
			return PA_ASKED;
		case PA_NOTIFY: {
			[[UNUserNotificationCenter currentNotificationCenter] requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound)
			                                                                    completionHandler:^(BOOL ok, NSError *err) { dispatch_semaphore_signal(s); }];
			pa_wait(s, 120);
			return PA_ASKED;
		}
		case PA_CALENDARS: {
			[[EKEventStore new] requestFullAccessToEventsWithCompletion:^(BOOL ok, NSError *err) { dispatch_semaphore_signal(s); }];
			pa_wait(s, 120);
			return PA_ASKED;
		}
		case PA_REMINDERS: {
			[[EKEventStore new] requestFullAccessToRemindersWithCompletion:^(BOOL ok, NSError *err) { dispatch_semaphore_signal(s); }];
			pa_wait(s, 120);
			return PA_ASKED;
		}
		case PA_CONTACTS: {
			[[CNContactStore new] requestAccessForEntityType:CNEntityTypeContacts completionHandler:^(BOOL ok, NSError *err) { dispatch_semaphore_signal(s); }];
			pa_wait(s, 120);
			return PA_ASKED;
		}
		case PA_MIC: {
			[AVCaptureDevice requestAccessForMediaType:AVMediaTypeAudio completionHandler:^(BOOL ok) { dispatch_semaphore_signal(s); }];
			pa_wait(s, 120);
			return PA_ASKED;
		}
		case PA_CAMERA: {
			[AVCaptureDevice requestAccessForMediaType:AVMediaTypeVideo completionHandler:^(BOOL ok) { dispatch_semaphore_signal(s); }];
			pa_wait(s, 120);
			return PA_ASKED;
		}
		}
		return PA_NO_API;
	}
}
*/
import "C"

import (
	"github.com/BorysSerbyn/process-automator/core/internal/permissions"
)

// checkable are the permissions macOS has a public way to check, and ask
// for. Full Disk Access and Automation have none: they can't be checked
// (Automation is asked for per app it sends events to).
var checkable = map[string]C.int{
	"input-monitoring": C.PA_INPUT,
	"accessibility":    C.PA_AX,
	"screen-recording": C.PA_SCREEN,
	"notifications":    C.PA_NOTIFY,
	"calendars":        C.PA_CALENDARS,
	"reminders":        C.PA_REMINDERS,
	"contacts":         C.PA_CONTACTS,
	"microphone":       C.PA_MIC,
	"camera":           C.PA_CAMERA,
}

// CheckPermission asks macOS whether this process may do what a permission
// allows. macOS answers for the process it's attributed to: the app for the
// scheduler (launched from the app's bundle, signed as the app) and what it
// runs; the terminal for a command typed in one. It never shows a prompt.
func CheckPermission(name string) permissions.Status {
	which, ok := checkable[name]
	switch {
	case ok:
	case permissions.Known(name):
		return permissions.Unknown
	default:
		return permissions.Unsupported
	}
	switch C.pa_check(which) {
	case C.PA_GRANTED:
		return permissions.Granted
	case C.PA_MISSING:
		return permissions.Missing
	default:
		return permissions.Unknown
	}
}

// noAPI is why macOS can't be asked for a permission it has no request for.
const noAPI = "macOS has no way for an app to ask for it: it's turned on in System Settings"

// CanRequestPermission says whether macOS can be asked for a permission
// from this process, and why not.
func CanRequestPermission(name string) (ok bool, why string) {
	which, known := checkable[name]
	if !known {
		return false, noAPI
	}
	switch C.pa_can_request(which) {
	case C.PA_ASKED:
		return true, ""
	case C.PA_NO_BUNDLE:
		return false, "only the installed app can ask: this executable isn't inside its bundle"
	case C.PA_NO_USAGE:
		return false, "the app's Info.plist doesn't say why it needs it, so macOS won't ask"
	default:
		return false, noAPI
	}
}

// RequestPermission asks macOS to grant a permission (CanRequestPermission
// said it can) to the app this process is attributed to: the first time,
// macOS shows its prompt; after that only System Settings changes it. It may
// wait for the answer, up to two minutes.
func RequestPermission(name string) {
	if which, ok := checkable[name]; ok {
		C.pa_request(which)
	}
}
