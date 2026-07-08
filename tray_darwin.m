#import <Cocoa/Cocoa.h>

@interface TrayHandler : NSObject
- (void)showWindow:(id)sender;
- (void)hideWindow:(id)sender;
- (void)applicationDidFinishLaunching:(NSNotification *)notification;
- (void)applicationDidHide:(NSNotification *)notification;
- (void)windowDidBecomeKey:(NSNotification *)notification;
- (void)windowDidResignKey:(NSNotification *)notification;
@end

static TrayHandler *trayHandler;
static NSStatusItem *statusItem;
static NSMenu *trayMenu;
static NSData *trayIconData;
static BOOL trayCreated = NO;

// Old anonymous status item positions can survive bundle-id changes and leave
// the item logically present but not visible in the menu bar.
static void clearLegacyStatusItemDefaults(void) {
    NSUserDefaults *defaults = [NSUserDefaults standardUserDefaults];
    NSArray *keys = [[defaults dictionaryRepresentation] allKeys];
    NSInteger removed = 0;
    for (NSString *key in keys) {
        if ([key hasPrefix:@"NSStatusItem Preferred Position"]) {
            [defaults removeObjectForKey:key];
            removed++;
        }
    }
    if (removed > 0) {
        [defaults synchronize];
        NSLog(@"[LingmaTap-Tray] removed %ld legacy status item position default(s)", (long)removed);
    }
}

static void createTrayMenuIfNeeded(void) {
    if (trayMenu != nil) {
        return;
    }

    trayMenu = [[NSMenu alloc] initWithTitle:@"Lingma Tap"];

    NSMenuItem *showItem = [[NSMenuItem alloc] initWithTitle:@"Show Window" action:@selector(showWindow:) keyEquivalent:@""];
    [showItem setTarget:trayHandler];
    [trayMenu addItem:showItem];

    NSMenuItem *hideItem = [[NSMenuItem alloc] initWithTitle:@"Hide Window" action:@selector(hideWindow:) keyEquivalent:@""];
    [hideItem setTarget:trayHandler];
    [trayMenu addItem:hideItem];

    [trayMenu addItem:[NSMenuItem separatorItem]];

    NSMenuItem *quitItem = [[NSMenuItem alloc] initWithTitle:@"Quit Lingma Tap" action:@selector(terminate:) keyEquivalent:@""];
    [quitItem setTarget:NSApp];
    [trayMenu addItem:quitItem];
}

static void configureTrayButton(void) {
    if (statusItem == nil) {
        NSLog(@"[LingmaTap-Tray] configure skipped: statusItem is nil");
        return;
    }
    if (statusItem.button == nil) {
        NSLog(@"[LingmaTap-Tray] configure skipped: statusItem.button is nil");
        return;
    }

    statusItem.length = NSVariableStatusItemLength;
    NSImage *image = nil;
    if (trayIconData != nil) {
        image = [[[NSImage alloc] initWithData:trayIconData] autorelease];
    }
    if (image != nil) {
        [image setSize:NSMakeSize(18, 18)];
        [image setTemplate:YES];
        [statusItem.button setImage:image];
        [statusItem.button setImagePosition:NSImageLeft];
        [statusItem.button setTitle:@""];
    } else {
        [statusItem.button setImage:nil];
        [statusItem.button setImagePosition:NSNoImage];
        [statusItem.button setTitle:@"Lingma Tap"];
    }
    [statusItem.button setToolTip:@"Lingma Tap"];
    [statusItem.button setEnabled:YES];
    if ([statusItem.button respondsToSelector:@selector(setAccessibilityIdentifier:)]) {
        [statusItem.button setAccessibilityIdentifier:@"LingmaTapStatusItem"];
    }
    if ([statusItem respondsToSelector:@selector(setVisible:)]) {
        [statusItem setVisible:YES];
    }
    [statusItem.button setNeedsDisplay:YES];

    BOOL visible = YES;
    if ([statusItem respondsToSelector:@selector(isVisible)]) {
        visible = [statusItem isVisible];
    }
    NSLog(@"[LingmaTap-Tray] configured status item title='%@' image=%d menu=%d length=%.1f visible=%d hidden=%d alpha=%.2f frame=%@ window=%@ screen=%@",
          [statusItem.button title],
          image != nil,
          statusItem.menu != nil,
          [statusItem length],
          visible,
          [statusItem.button isHidden],
          [statusItem.button alphaValue],
          NSStringFromRect([statusItem.button frame]),
          [statusItem.button window],
          [[statusItem.button window] screen]);
}

static void createTrayIfNeeded(void) {
    if (trayCreated) {
        configureTrayButton();
        return;
    }
    trayCreated = YES;

    clearLegacyStatusItemDefaults();
    statusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
#if !__has_feature(objc_arc)
    [statusItem retain];
    NSLog(@"[LingmaTap-Tray] statusItem retained manually (non-ARC)");
#endif

    if (statusItem == nil || statusItem.button == nil) {
        NSLog(@"[LingmaTap-Tray] failed to create visible status item");
        return;
    }

    createTrayMenuIfNeeded();
    statusItem.menu = trayMenu;
    configureTrayButton();
    NSLog(@"[LingmaTap-Tray] created status item after launch");

    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(1 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
        NSLog(@"[LingmaTap-Tray] refreshing status item after delay");
        configureTrayButton();
    });
}

// Exported from Go
extern void goShowWindow();
extern void goHideWindow();

void initTray(const unsigned char* iconData, int iconLength) {
    NSLog(@"[LingmaTap-Tray] initTray called with icon length: %d", iconLength);
    NSData *iconDataCopy = [[NSData alloc] initWithBytes:iconData length:iconLength];
    dispatch_async(dispatch_get_main_queue(), ^{
        NSLog(@"[LingmaTap-Tray] initTray running on main queue; appRunning=%d", [NSApp isRunning]);
        if (trayIconData == nil) {
            trayIconData = iconDataCopy;
        } else {
#if !__has_feature(objc_arc)
            [iconDataCopy release];
#endif
        }

        if (trayHandler == nil) {
            trayHandler = [[TrayHandler alloc] init];
#if !__has_feature(objc_arc)
            [trayHandler retain];
            NSLog(@"[LingmaTap-Tray] trayHandler retained manually (non-ARC)");
#endif
            [[NSNotificationCenter defaultCenter] addObserver:trayHandler
                                                     selector:@selector(applicationDidFinishLaunching:)
                                                         name:NSApplicationDidFinishLaunchingNotification
                                                       object:NSApp];

            [[NSNotificationCenter defaultCenter] addObserver:trayHandler
                                                     selector:@selector(applicationDidHide:)
                                                         name:NSApplicationDidHideNotification
                                                       object:NSApp];

            [[NSNotificationCenter defaultCenter] addObserver:trayHandler
                                                     selector:@selector(windowDidBecomeKey:)
                                                         name:NSWindowDidBecomeKeyNotification
                                                       object:nil];

            [[NSNotificationCenter defaultCenter] addObserver:trayHandler
                                                     selector:@selector(windowDidResignKey:)
                                                         name:NSWindowDidResignKeyNotification
                                                       object:nil];
        }

        if ([NSApp isRunning]) {
            createTrayIfNeeded();
            return;
        }

        NSLog(@"[LingmaTap-Tray] app is not running yet; waiting for NSApplicationDidFinishLaunching");
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(1 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
            NSLog(@"[LingmaTap-Tray] delayed creation fallback; appRunning=%d", [NSApp isRunning]);
            createTrayIfNeeded();
        });
    });
}

static BOOL hasVisibleWindows(void) {
    for (NSWindow *window in [NSApp windows]) {
        if ([window isMiniaturized]) {
            continue;
        }
        if ([window isVisible] && [window canBecomeKeyWindow]) {
            // Ensure we only count actual app windows (titled) rather than status item helper panels
            if (window.styleMask & NSWindowStyleMaskTitled) {
                return YES;
            }
        }
    }
    return NO;
}

@implementation TrayHandler
- (void)showWindow:(id)sender {
    NSLog(@"[LingmaTap-Tray] showWindow action triggered; transitioning to Regular activation policy");
    [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
    [NSApp unhide:nil];
    [NSApp activateIgnoringOtherApps:YES];
    goShowWindow();

    // Nudge the menu bar and focus on the main queue with a slight delay
    // to ensure Wails has finished showing the window.
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(200 * NSEC_PER_MSEC)), dispatch_get_main_queue(), ^{
        NSLog(@"[LingmaTap-Tray] showWindow activation nudge");
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        [NSApp activateIgnoringOtherApps:YES];
    });
}

- (void)hideWindow:(id)sender {
    NSLog(@"[LingmaTap-Tray] hideWindow action triggered");
    goHideWindow();
}

- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    NSLog(@"[LingmaTap-Tray] application did finish launching; creating status item");
    createTrayIfNeeded();
}

- (void)applicationDidHide:(NSNotification *)notification {
    NSLog(@"[LingmaTap-Tray] application hidden; switching activation policy to accessory and unhiding application");
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    
    // Hide all titled windows immediately so they don't flash when we unhide the application
    for (NSWindow *window in [NSApp windows]) {
        if (window.styleMask & NSWindowStyleMaskTitled) {
            [window orderOut:nil];
        }
    }
    
    [NSApp unhide:nil];
    configureTrayButton();
}

- (void)windowDidBecomeKey:(NSNotification *)notification {
    NSLog(@"[LingmaTap-Tray] windowDidBecomeKey notification; checking activation policy");
    if ([NSApp activationPolicy] != NSApplicationActivationPolicyRegular) {
        NSLog(@"[LingmaTap-Tray] window became key, switching activation policy to regular");
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        [NSApp activateIgnoringOtherApps:YES];
        configureTrayButton();
    }
}

- (void)windowDidResignKey:(NSNotification *)notification {
    NSLog(@"[LingmaTap-Tray] windowDidResignKey notification; checking if visible windows remain");
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(100 * NSEC_PER_MSEC)), dispatch_get_main_queue(), ^{
        if (!hasVisibleWindows()) {
            NSLog(@"[LingmaTap-Tray] no visible windows left; switching activation policy to accessory");
            [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
            configureTrayButton();
        } else {
            NSLog(@"[LingmaTap-Tray] visible windows still exist; keeping regular policy");
        }
    });
}
@end
