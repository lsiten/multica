#import "../native_internal.h"
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>

@interface Fixture : NSObject
@property NSInteger presses;
- (void)press:(id)sender;
@end
@implementation Fixture
- (void)press:(id)sender {
  self.presses++;
}
@end

// Explicit accessibility implementations make this a controlled semantic-input
// fixture.
@interface FixtureButton : NSButton
@end
@implementation FixtureButton
- (BOOL)accessibilityPerformPress {
  return [NSApp sendAction:self.action to:self.target from:self];
}
@end
@interface FixtureText : NSTextField
@end
@implementation FixtureText
- (id)accessibilityValue {
  return self.stringValue;
}
- (void)setAccessibilityValue:(id)value {
  if ([value isKindOfClass:NSString.class])
    self.stringValue = value;
}
@end

@interface Canvas : NSView
@property NSInteger scrolls, drags, keys;
@end
@implementation Canvas
- (BOOL)acceptsFirstResponder {
  return YES;
}
- (void)scrollWheel:(NSEvent *)event {
  self.scrolls++;
  [self setNeedsDisplay:YES];
}
- (void)mouseDragged:(NSEvent *)event {
  self.drags++;
  [self setNeedsDisplay:YES];
}
- (void)keyDown:(NSEvent *)event {
  self.keys++;
  [self setNeedsDisplay:YES];
}
- (void)drawRect:(NSRect)dirty {
  [[NSColor colorWithCalibratedRed:.15 green:.4 blue:.7 alpha:1] setFill];
  NSRectFill(self.bounds);
  NSString *text =
      [NSString stringWithFormat:@"keys=%ld scrolls=%ld drags=%ld", self.keys,
                                 self.scrolls, self.drags];
  [text drawAtPoint:NSMakePoint(10, 20)
      withAttributes:@{NSForegroundColorAttributeName : NSColor.whiteColor}];
}
@end

int main(int argc, const char **argv) {
  @autoreleasepool {
    BOOL serve = argc == 2 && strcmp(argv[1], "--serve") == 0;
    [NSApplication sharedApplication];
    [NSApp setActivationPolicy:serve ? NSApplicationActivationPolicyAccessory
                                     : NSApplicationActivationPolicyProhibited];
    NSWindow *window =
        [[NSWindow alloc] initWithContentRect:NSMakeRect(100, 100, 640, 480)
                                    styleMask:NSWindowStyleMaskTitled |
                                              NSWindowStyleMaskClosable |
                                              NSWindowStyleMaskResizable
                                      backing:NSBackingStoreBuffered
                                        defer:NO];
    window.title = @"Multica AppControl Fixture";
    Fixture *fixture = [Fixture new];
    NSButton *button =
        [[FixtureButton alloc] initWithFrame:NSMakeRect(20, 390, 180, 40)];
    button.title = @"Increment fixture";
    button.target = fixture;
    button.action = @selector(press:);
    button.accessibilityIdentifier = @"fixture-button";
    NSTextField *field =
        [[FixtureText alloc] initWithFrame:NSMakeRect(20, 340, 560, 30)];
    field.accessibilityIdentifier = @"fixture-text";
    Canvas *canvas =
        [[Canvas alloc] initWithFrame:NSMakeRect(20, 20, 600, 300)];
    canvas.accessibilityLabel = @"Fixture input counters";
    [window.contentView addSubview:button];
    [window.contentView addSubview:field];
    [window.contentView addSubview:canvas];
    if (serve) {
      [window orderBack:nil];
      [NSApp run];
      return 0;
    }
    pid_t beforePID =
        NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier;
    CGEventRef before = CGEventCreate(NULL);
    CGPoint cursorBefore = CGEventGetLocation(before);
    CFRelease(before);
    BOOL pressed = [button accessibilityPerformPress];
    NSString *unicode = @"中文 é 👋";
    [field setAccessibilityValue:unicode];
    BOOL textVerified = [[field accessibilityValue] isEqual:unicode];
    CGEventRef after = CGEventCreate(NULL);
    CGPoint cursorAfter = CGEventGetLocation(after);
    CFRelease(after);
    pid_t afterPID =
        NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier;
    NSDictionary *identity = ACProcess(getpid());
    ACSession *session = [ACSession new];
    session.pressed = [NSMutableDictionary new];
    session.uncertainInput = [NSMutableDictionary new];
    NSDictionary *owner = @{@"RuntimeID" : @"fixture"},
                 *other = @{@"RuntimeID" : @"other"};
    BOOL nativeIdentity = identity != nil && !ACProcessEnded(identity);
    BOOL blocked = NO, isolated = NO, reused = NO;
    if (nativeIdentity) {
      session.uncertainInput[@"fixture-down"] =
          @{@"Process" : identity, @"Resource" : owner};
      blocked = ACInputQuiescent(session, owner) != nil;
      isolated = ACInputQuiescent(session, other) == nil;
      NSMutableDictionary *stale = [identity mutableCopy];
      stale[@"Start"] =
          [identity[@"Start"] stringByAppendingString:@":old-incarnation"];
      session.uncertainInput[@"fixture-down"] =
          @{@"Process" : stale, @"Resource" : owner};
      reused = ACInputQuiescent(session, owner) == nil &&
               session.uncertainInput.count == 0;
    }
    NSDictionary *result = @{
      @"scope" :
          @"hidden fixture Cocoa self-actions and native fence state unit",
      @"native_identity" : [NSNumber numberWithBool:nativeIdentity],
      @"uncertain_input_blocks" : [NSNumber numberWithBool:blocked],
      @"uncertain_input_other_resource_clear" :
          [NSNumber numberWithBool:isolated],
      @"pid_reuse_clears_old_fence" : [NSNumber numberWithBool:reused],
      @"frontmost_pid_before" : @(beforePID),
      @"frontmost_pid_after" : @(afterPID),
      @"cursor_before" : @{@"x" : @(cursorBefore.x), @"y" : @(cursorBefore.y)},
      @"cursor_after" : @{@"x" : @(cursorAfter.x), @"y" : @(cursorAfter.y)},
      @"button_press" :
          [NSNumber numberWithBool:pressed && fixture.presses == 1],
      @"unicode_value" : [NSNumber numberWithBool:textVerified],
      @"frontmost_unchanged" : [NSNumber numberWithBool:beforePID == afterPID],
      @"cursor_unchanged" : [NSNumber
          numberWithBool:CGPointEqualToPoint(cursorBefore, cursorAfter)],
      @"external_ax_verified" : @NO,
      @"per_pid_input_verified" : @NO,
      @"accessibility_permission" :
          [NSNumber numberWithBool:AXIsProcessTrusted()],
      @"screen_recording_permission" :
          [NSNumber numberWithBool:CGPreflightScreenCaptureAccess()]
    };
    NSData *data =
        [NSJSONSerialization dataWithJSONObject:result
                                        options:NSJSONWritingPrettyPrinted
                                          error:nil];
    fwrite(data.bytes, 1, data.length, stdout);
    fputc('\n', stdout);
    return nativeIdentity && blocked && isolated && reused && pressed &&
                   fixture.presses == 1 && textVerified &&
                   beforePID == afterPID &&
                   CGPointEqualToPoint(cursorBefore, cursorAfter)
               ? 0
               : 1;
  }
}
