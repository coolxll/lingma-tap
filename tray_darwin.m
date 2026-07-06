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
    dispatch_async(dispatch_get_main_queue(), ^{
        statusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
        
        NSData *data = [NSData dataWithBytes:iconData length:iconLength];
        NSImage *image = [[NSImage alloc] initWithData:data];
        if (image) {
            [image setSize:NSMakeSize(18, 18)]; // standard macOS menu bar size
            [image setTemplate:YES];           // adapts to light/dark mode automatically
            [statusItem.button setImage:image];
        } else {
            // Fallback to text if image decoding fails
            [statusItem.button setTitle:@"🔌 Lingma Tap"];
        }
        
        trayHandler = [[TrayHandler alloc] init];
        
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
