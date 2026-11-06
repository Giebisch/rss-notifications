#import <AppKit/AppKit.h>
#import <ServiceManagement/ServiceManagement.h>
#import <UserNotifications/UserNotifications.h>
#include <stdbool.h>

static NSString *str(const char *s) {
	return [NSString stringWithUTF8String:s] ?: @"";
}

static void openLink(NSString *link) {
	NSURL *url = [NSURL URLWithString:link];
	if (url) {
		[[NSWorkspace sharedWorkspace] openURL:url];
	}
}

@interface NotificationDelegate : NSObject <UNUserNotificationCenterDelegate>
@end

@implementation NotificationDelegate
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
       willPresentNotification:(UNNotification *)notification
         withCompletionHandler:(void (^)(UNNotificationPresentationOptions))completionHandler {
	completionHandler(UNNotificationPresentationOptionBanner | UNNotificationPresentationOptionList |
	                  UNNotificationPresentationOptionSound);
}

- (void)userNotificationCenter:(UNUserNotificationCenter *)center
    didReceiveNotificationResponse:(UNNotificationResponse *)response
             withCompletionHandler:(void (^)(void))completionHandler {
	if ([response.actionIdentifier isEqualToString:UNNotificationDefaultActionIdentifier]) {
		openLink(response.notification.request.content.userInfo[@"url"]);
	}
	completionHandler();
}
@end

// UNUserNotificationCenter only keeps a weak reference to its delegate.
static NotificationDelegate *delegate;

void notifyInit(void) {
	delegate = [NotificationDelegate new];
	UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
	center.delegate = delegate;
	[center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound)
	                      completionHandler:^(BOOL granted, NSError *error) {
		                      if (error) {
			                      NSLog(@"notification authorization: %@", error);
		                      }
	                      }];
}

void notify(const char *title, const char *body, const char *url, const char *thread) {
	@autoreleasepool {
		UNMutableNotificationContent *content = [UNMutableNotificationContent new];
		content.title = str(title);
		content.body = str(body);
		content.threadIdentifier = str(thread);
		content.userInfo = @{@"url" : str(url)};
		content.sound = [UNNotificationSound defaultSound];
		UNNotificationRequest *request = [UNNotificationRequest requestWithIdentifier:[NSUUID UUID].UUIDString
		                                                                      content:content
		                                                                      trigger:nil];
		[[UNUserNotificationCenter currentNotificationCenter]
		    addNotificationRequest:request
		     withCompletionHandler:^(NSError *error) {
			     if (error) {
				     NSLog(@"notification: %@", error);
			     }
		     }];
	}
}

void openURL(const char *url) {
	@autoreleasepool {
		openLink(str(url));
	}
}

// Text fields get ⌘X/⌘C/⌘V/⌘A only through key equivalents in the main menu, which a menu bar app
// doesn't have. The menu itself is never shown because the app has no menu bar of its own.
static void installEditMenu(void) {
	if (NSApp.mainMenu) {
		return;
	}
	NSMenu *edit = [[NSMenu alloc] initWithTitle:@"Edit"];
	[edit addItemWithTitle:@"Undo" action:@selector(undo:) keyEquivalent:@"z"];
	[edit addItemWithTitle:@"Redo" action:@selector(redo:) keyEquivalent:@"Z"];
	[edit addItemWithTitle:@"Cut" action:@selector(cut:) keyEquivalent:@"x"];
	[edit addItemWithTitle:@"Copy" action:@selector(copy:) keyEquivalent:@"c"];
	[edit addItemWithTitle:@"Paste" action:@selector(paste:) keyEquivalent:@"v"];
	[edit addItemWithTitle:@"Select All" action:@selector(selectAll:) keyEquivalent:@"a"];
	NSMenuItem *editItem = [NSMenuItem new];
	editItem.submenu = edit;
	NSApp.mainMenu = [NSMenu new];
	[NSApp.mainMenu addItem:editItem];
}

// Returns NULL if cancelled, otherwise a malloc'd string the caller must free.
// Must not be called from the main thread.
const char *prompt(const char *title, const char *message, const char *button, const char *placeholder,
                   const char *value, bool multiline) {
	__block char *result = NULL;
	dispatch_sync(dispatch_get_main_queue(), ^{
		installEditMenu();

		NSTextField *field = [[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 460, multiline ? 80 : 24)];
		field.placeholderString = str(placeholder);
		field.stringValue = str(value);
		if (multiline) {
			// A wrapping field instead of a scrolling one, so long URLs stay fully visible.
			field.cell.wraps = YES;
			field.cell.scrollable = NO;
			field.cell.usesSingleLineMode = NO;
			field.lineBreakMode = NSLineBreakByCharWrapping;
		}

		NSAlert *alert = [NSAlert new];
		alert.messageText = str(title);
		alert.informativeText = str(message);
		[alert addButtonWithTitle:str(button)];
		[alert addButtonWithTitle:@"Cancel"];
		alert.accessoryView = field;
		alert.window.initialFirstResponder = field;

		// Menu bar apps are never active on their own, so the alert would open behind other windows.
		if (@available(macOS 14, *)) {
			[NSApp activate];
		} else {
			[NSApp activateIgnoringOtherApps:YES];
		}
		if ([alert runModal] == NSAlertFirstButtonReturn) {
			result = strdup(field.stringValue.UTF8String);
		}
	});
	return result;
}

bool loginItemEnabled(void) {
	return SMAppService.mainAppService.status == SMAppServiceStatusEnabled;
}

// Returns NULL on success, otherwise a malloc'd error message the caller must free.
const char *setLoginItem(bool enabled) {
	@autoreleasepool {
		NSError *error = nil;
		BOOL ok = enabled ? [SMAppService.mainAppService registerAndReturnError:&error]
		                  : [SMAppService.mainAppService unregisterAndReturnError:&error];
		return ok ? NULL : strdup(error.localizedDescription.UTF8String);
	}
}
