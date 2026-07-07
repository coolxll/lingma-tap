#import <Cocoa/Cocoa.h>

@interface TrayHandler : NSObject
- (void)showWindow:(id)sender;
- (void)hideWindow:(id)sender;
@end

static TrayHandler *trayHandler;
static NSStatusItem *statusItem;

// Exported from Go
extern void goShowWindow();
extern void goHideWindow();

void initTray(const unsigned char* iconData, int iconLength) {
    NSLog(@"[LingmaTap-Tray] initTray called with icon length: %d", iconLength);
    // Copy bytes into Obj-C-owned storage before dispatching asynchronously,
    // so the block captures an NSData object instead of a pointer into
    // Go-managed memory (which may be invalid after C.initTray returns).
    NSData *iconDataCopy = [NSData dataWithBytes:iconData length:iconLength];
    dispatch_async(dispatch_get_main_queue(), ^{
        NSLog(@"[LingmaTap-Tray] initTray running on main queue");
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
        
        statusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
#if !__has_feature(objc_arc)
        [statusItem retain];
        NSLog(@"[LingmaTap-Tray] statusItem retained manually (non-ARC)");
#endif
        
        NSImage *image = [[NSImage alloc] initWithData:iconDataCopy];
        if (image) {
            NSLog(@"[LingmaTap-Tray] initTray successfully decoded image");
            [image setSize:NSMakeSize(18, 18)]; // standard macOS menu bar size
            [image setTemplate:YES];           // adapts to light/dark mode automatically
            [statusItem.button setImage:image];
        } else {
            NSLog(@"[LingmaTap-Tray] initTray image decoding failed, falling back to text");
            [statusItem.button setTitle:@"🔌 Lingma Tap"];
        }
        
        trayHandler = [[TrayHandler alloc] init];
#if !__has_feature(objc_arc)
        [trayHandler retain];
        NSLog(@"[LingmaTap-Tray] trayHandler retained manually (non-ARC)");
#endif
        
        NSMenu *menu = [[NSMenu alloc] init];
        
        NSMenuItem *showItem = [[NSMenuItem alloc] initWithTitle:@"Show Window" action:@selector(showWindow:) keyEquivalent:@""];
        [showItem setTarget:trayHandler];
        [menu addItem:showItem];
        
        NSMenuItem *hideItem = [[NSMenuItem alloc] initWithTitle:@"Hide Window" action:@selector(hideWindow:) keyEquivalent:@""];
        [hideItem setTarget:trayHandler];
        [menu addItem:hideItem];
        
        [menu addItem:[NSMenuItem separatorItem]];
        
        NSMenuItem *quitItem = [[NSMenuItem alloc] initWithTitle:@"Quit" action:@selector(terminate:) keyEquivalent:@""];
        [quitItem setTarget:NSApp];
        [menu addItem:quitItem];
        
        statusItem.menu = menu;
        NSLog(@"[LingmaTap-Tray] initTray completed successfully");
    });
}

@implementation TrayHandler
- (void)showWindow:(id)sender {
    goShowWindow();
}
- (void)hideWindow:(id)sender {
    goHideWindow();
}
@end
