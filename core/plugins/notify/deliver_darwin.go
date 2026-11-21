//go:build darwin && cgo

package notify

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework UserNotifications
#include <stdlib.h>
#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>

// pa_wait waits for a completion handler, up to secs; it reports whether it came.
static BOOL pa_wait(dispatch_semaphore_t s, int secs) {
	return dispatch_semaphore_wait(s, dispatch_time(DISPATCH_TIME_NOW, (int64_t)secs * NSEC_PER_SEC)) == 0;
}

// pa_notify posts a notification as the app bundle this executable is in.
// It returns NULL when it was posted, else what went wrong (to free).
static char *pa_notify(const char *title, const char *body, int urgent) {
	@autoreleasepool {
		NSBundle *app = [NSBundle mainBundle];
		// Outside an app bundle (a development build) UserNotifications
		// raises rather than failing: don't call it.
		if (app.bundleIdentifier == nil || ![app.bundleURL.pathExtension isEqualToString:@"app"]) {
			return strdup("desktop notifications need the installed app: this executable isn't inside its bundle");
		}
		UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
		dispatch_semaphore_t s = dispatch_semaphore_create(0);

		__block UNAuthorizationStatus status = UNAuthorizationStatusNotDetermined;
		[center getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *settings) {
			status = settings.authorizationStatus;
			dispatch_semaphore_signal(s);
		}];
		if (!pa_wait(s, 10)) return strdup("macOS didn't say whether the app may notify");
		if (status == UNAuthorizationStatusNotDetermined) {
			// macOS asks me once, for the app; wait a while for the answer.
			__block BOOL granted = NO;
			[center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound)
			                      completionHandler:^(BOOL ok, NSError *err) {
				granted = ok;
				dispatch_semaphore_signal(s);
			}];
			if (!pa_wait(s, 120) || !granted) return strdup("macOS doesn't allow the app's notifications (not answered, or turned down)");
		} else if (status == UNAuthorizationStatusDenied) {
			NSString *msg = [NSString stringWithFormat:@"notifications are turned off for %@ (System Settings > Notifications)",
			                 [app objectForInfoDictionaryKey:@"CFBundleName"] ?: app.bundleIdentifier];
			return strdup(msg.UTF8String);
		}

		UNMutableNotificationContent *content = [UNMutableNotificationContent new];
		content.title = [NSString stringWithUTF8String:title];
		content.body = [NSString stringWithUTF8String:body];
		content.sound = [UNNotificationSound defaultSound];
		// Without the time-sensitive entitlement macOS shows it as active.
		if (urgent) content.interruptionLevel = UNNotificationInterruptionLevelTimeSensitive;
		UNNotificationRequest *req = [UNNotificationRequest requestWithIdentifier:[NSUUID UUID].UUIDString content:content trigger:nil];
		__block NSString *failed = nil;
		[center addNotificationRequest:req withCompletionHandler:^(NSError *err) {
			if (err != nil) failed = err.localizedDescription;
			dispatch_semaphore_signal(s);
		}];
		if (!pa_wait(s, 10)) return strdup("macOS didn't confirm the notification");
		if (failed != nil) return strdup(failed.UTF8String);
		return NULL;
	}
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// deliver posts n through UserNotifications, as the app: the core runs from
// inside the app's bundle, signed with its identifier, so it shares the
// app's permission to notify.
func deliver(n Note) error {
	title, body := C.CString(n.Title), C.CString(n.Body)
	defer C.free(unsafe.Pointer(title))
	defer C.free(unsafe.Pointer(body))
	urgent := C.int(0)
	if n.Urgent {
		urgent = 1
	}
	if msg := C.pa_notify(title, body, urgent); msg != nil {
		defer C.free(unsafe.Pointer(msg))
		return errors.New(C.GoString(msg))
	}
	return nil
}
