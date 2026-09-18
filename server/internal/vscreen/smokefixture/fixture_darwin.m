//go:build darwin && cgo

#import "bridge.h"
#import <AppKit/AppKit.h>
#import <CoreGraphics/CoreGraphics.h>
#import <CoreServices/CoreServices.h>
#include <libproc.h>
#include <sys/proc_info.h>
#include <unistd.h>
#include <mach/mach_time.h>
#include "performance_marker.h"

static NSString *processStart(int pid) {
  struct proc_bsdinfo info = {0};
  if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, sizeof(info)) != sizeof(info) || info.pbi_uid != getuid()) return nil;
  return [NSString stringWithFormat:@"%llu:%llu", info.pbi_start_tvsec, info.pbi_start_tvusec];
}
char *multica_smoke_fixture_start(int pid) {
  @autoreleasepool { NSString *start = processStart(pid); return start ? strdup(start.UTF8String) : NULL; }
}
int multica_smoke_fixture_register(const char *path) {
  @autoreleasepool {
    if (!getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE") || strcmp(getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE"), "1")) return 1;
    NSURL *url = [NSURL fileURLWithPath:@(path)];
    NSBundle *bundle = [NSBundle bundleWithURL:url];
    if (![bundle.bundleIdentifier hasPrefix:@"ai.multica.smoke."] || LSRegisterURL((__bridge CFURLRef)url, true) != noErr) return 1;
    NSURL *registered = [NSWorkspace.sharedWorkspace URLForApplicationWithBundleIdentifier:bundle.bundleIdentifier];
    return [registered.URLByResolvingSymlinksInPath.path isEqual:url.URLByResolvingSymlinksInPath.path] ? 0 : 1;
  }
}
char *multica_smoke_fixture_foreground(void) {
  @autoreleasepool {
    if (!getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE") || strcmp(getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE"), "1")) return NULL;
    pid_t pid = NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier;
    CGEventRef cursor = CGEventCreate(NULL);
    if (!cursor || pid <= 0) { if (cursor) CFRelease(cursor); return NULL; }
    CGPoint point = CGEventGetLocation(cursor); CFRelease(cursor);
    uint32_t identifier = 0;
    NSArray *windows = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly, kCGNullWindowID));
    for (NSDictionary *window in windows) {
      if ([window[(id)kCGWindowOwnerPID] intValue] == pid && [window[(id)kCGWindowLayer] intValue] == 0) { identifier = [window[(id)kCGWindowNumber] unsignedIntValue]; break; }
    }
    NSDictionary *value = @{@"pid":@(pid), @"window_id":@(identifier), @"cursor_x":@(point.x), @"cursor_y":@(point.y)};
    NSData *json = [NSJSONSerialization dataWithJSONObject:value options:0 error:nil];
    return json ? strndup(json.bytes, json.length) : NULL;
  }
}

@class MVSSmokeApp;
@interface MVSSmokeText : NSTextField
@property(weak) MVSSmokeApp *owner;
@end
@interface MVSSmokeButton : NSButton
@end
@interface MVSSmokeCanvas : NSView
@property(weak) MVSSmokeApp *owner;
@end
@interface MVSPerformanceCanvas : NSView
@property uint32_t sourceTag, sequence;
@end
@implementation MVSPerformanceCanvas
- (BOOL)isFlipped { return YES; }
- (void)drawRect:(NSRect)dirty {
  mach_timebase_info_data_t timebase; mach_timebase_info(&timebase);
  uint64_t stamp=(uint64_t)((__uint128_t)mach_continuous_time()*timebase.numer/timebase.denom);
  uint32_t sequence=++self.sequence;
  [[NSColor colorWithCalibratedRed:(sequence%127)/127.0 green:0.25 blue:0.6 alpha:1] setFill];NSRectFill(self.bounds);
  [[NSColor whiteColor] setFill];NSRectFill(NSMakeRect((sequence*7)%MAX(1,(int)self.bounds.size.width),110,80,100));
  uint8_t bytes[20];multica_performance_marker(bytes,self.sourceTag,sequence,stamp);
  CGContextSetShouldAntialias(NSGraphicsContext.currentContext.CGContext,false);
  for(int i=0;i<160;i++){[((bytes[i/8]>>(7-i%8))&1)?NSColor.whiteColor:NSColor.blackColor setFill];NSRectFill(NSMakeRect(16+(i%20)*8,16+(i/20)*8,8,8));}
}
@end
@interface MVSSmokeApp : NSObject
@property NSDictionary *config;
@property NSWindow *window;
@property MVSSmokeText *text;
@property NSTextField *counter;
@property NSUInteger presses, keys, scrolls, drags, humanStage;
@property(strong) NSData *lastPublished;
@property BOOL closed, writeFailed;
- (void)publish;
- (void)increment:(id)sender;
- (void)finish;
@end
@implementation MVSSmokeButton
- (BOOL)accessibilityPerformPress { return [NSApp sendAction:self.action to:self.target from:self]; }
@end
@implementation MVSSmokeText
- (NSString *)accessibilityTitle { return @"Multica smoke text"; }
- (id)accessibilityValue { return self.stringValue; }
- (void)setAccessibilityValue:(id)value {
  if ([value isKindOfClass:NSString.class]) { self.stringValue = value; [self.owner publish]; }
}
@end
@implementation MVSSmokeCanvas
- (BOOL)acceptsFirstResponder { return YES; }
- (void)drawRect:(NSRect)dirty { [[NSColor colorWithCalibratedRed:0.12 green:0.4 blue:0.7 alpha:1] setFill]; NSRectFill(self.bounds); }
@end
@implementation MVSSmokeApp
- (void)publish {
  CGRect bounds = CGRectZero;
  if (!self.closed && self.window.windowNumber > 0) {
    NSArray *windows = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionIncludingWindow, (CGWindowID)self.window.windowNumber));
    for (NSDictionary *window in windows) {
      if ([window[(id)kCGWindowOwnerPID] intValue] == getpid() && [window[(id)kCGWindowNumber] unsignedIntValue] == self.window.windowNumber) {
        CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)window[(id)kCGWindowBounds], &bounds); break;
      }
    }
  }
  NSNumber *displayID = self.closed ? @0 : self.window.screen.deviceDescription[@"NSScreenNumber"];
  NSMutableDictionary *state = [@{@"display_id":displayID ?: @0, @"bounds":@{@"x":@(bounds.origin.x),@"y":@(bounds.origin.y),@"width":@(bounds.size.width),@"height":@(bounds.size.height)}, @"human_stage":@(self.humanStage), @"nonce":self.config[@"nonce"], @"pid":@(getpid()), @"process_start":processStart(getpid()) ?: @"", @"window_id":@(self.closed ? 0 : MAX(0, self.window.windowNumber)), @"presses":@(self.presses), @"text":self.text.stringValue ?: @"", @"keys":@(self.keys), @"scrolls":@(self.scrolls), @"drags":@(self.drags), @"closed":@(self.closed)} mutableCopy];
  if (self.config[@"performance"] && !self.closed) {NSRect frame=self.window.frame,content=[self.window contentRectForFrameRect:frame];state[@"marker"]=@{@"x":@(bounds.origin.x+content.origin.x-frame.origin.x+16),@"y":@(bounds.origin.y+NSMaxY(frame)-NSMaxY(content)+16),@"cell_size":@8,@"columns":@20,@"source_tag":self.config[@"performance"][@"source_tag"]};}
  NSData *json = [NSJSONSerialization dataWithJSONObject:state options:0 error:nil];
  if (json && [json isEqualToData:self.lastPublished]) return;
  self.lastPublished = json;
  NSString *path = [self.config[@"directory"] stringByAppendingPathComponent:@"readback.json"];
  if (!json || ![json writeToFile:path options:NSDataWritingAtomic error:nil] || ![NSFileManager.defaultManager setAttributes:@{NSFilePosixPermissions:@0600} ofItemAtPath:path error:nil]) self.writeFailed = YES;
}
- (void)increment:(id)sender { self.presses++; self.counter.stringValue = [NSString stringWithFormat:@"presses=%lu", self.presses]; [self publish]; }
- (void)finish {
  if (self.closed) return;
  [self.window close]; self.closed = YES; [self publish];
  [NSApp stop:nil];
  [NSApp postEvent:[NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0] atStart:NO];
}
@end

int multica_smoke_fixture_run(const char *configuration) {
  @autoreleasepool {
    if (!getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE") || strcmp(getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE"), "1")) return 1;
    NSData *raw = [@(configuration) dataUsingEncoding:NSUTF8StringEncoding];
    NSDictionary *config = [NSJSONSerialization JSONObjectWithData:raw options:0 error:nil];
    if (!config || ![NSBundle.mainBundle.bundleIdentifier isEqual:config[@"bundle_id"]]) return 1;
    MVSSmokeApp *fixture = [MVSSmokeApp new]; fixture.config = config;
    NSString *stopPath = [config[@"directory"] stringByAppendingPathComponent:@"stop"];
    BOOL stopped = [[NSString stringWithContentsOfFile:stopPath encoding:NSUTF8StringEncoding error:nil] isEqual:config[@"nonce"]];
    if (stopped || ![processStart([config[@"owner_pid"] intValue]) isEqual:config[@"owner_start"]]) { fixture.closed = YES; [fixture publish]; return fixture.writeFailed ? 1 : 0; }
    [NSApplication sharedApplication]; [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    fixture.window = [[NSWindow alloc] initWithContentRect:NSMakeRect(100,100,640,440) styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskResizable backing:NSBackingStoreBuffered defer:NO];
    fixture.window.releasedWhenClosed = NO; fixture.window.title = @"Multica test-owned smoke fixture";
    MVSSmokeButton *button = [[MVSSmokeButton alloc] initWithFrame:NSMakeRect(20,350,260,40)];
    button.title = @"Multica smoke increment"; button.target = fixture; button.action = @selector(increment:);
    fixture.text = [[MVSSmokeText alloc] initWithFrame:NSMakeRect(20,300,580,30)]; fixture.text.owner = fixture; fixture.text.editable = YES;
    fixture.counter = [NSTextField labelWithString:@"presses=0"]; fixture.counter.frame = NSMakeRect(20,260,580,30);
    MVSSmokeCanvas *canvas = [[MVSSmokeCanvas alloc] initWithFrame:NSMakeRect(20,20,580,220)]; canvas.owner = fixture;
    [fixture.window.contentView addSubview:button]; [fixture.window.contentView addSubview:fixture.text]; [fixture.window.contentView addSubview:fixture.counter]; [fixture.window.contentView addSubview:canvas];
    id monitor = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskKeyDown | NSEventMaskScrollWheel | NSEventMaskLeftMouseDragged | NSEventMaskRightMouseDragged | NSEventMaskOtherMouseDragged handler:^NSEvent *(NSEvent *event) {
      if (event.type == NSEventTypeKeyDown) fixture.keys++;
      else if (event.type == NSEventTypeScrollWheel) fixture.scrolls++;
      else fixture.drags++;
      [fixture publish]; return event;
    }];
    NSTimer *animation=nil;
    if(config[@"performance"]) {
      [fixture.window setContentSize:NSMakeSize(1600,900)];
      MVSPerformanceCanvas *performance=[[MVSPerformanceCanvas alloc] initWithFrame:fixture.window.contentView.bounds];
      performance.autoresizingMask=NSViewWidthSizable|NSViewHeightSizable;performance.sourceTag=[config[@"performance"][@"source_tag"] unsignedIntValue];
      [fixture.window.contentView.subviews makeObjectsPerformSelector:@selector(removeFromSuperview)];[fixture.window.contentView addSubview:performance];
      animation=[NSTimer scheduledTimerWithTimeInterval:1.0/30.0 repeats:YES block:^(NSTimer *timer){[performance setNeedsDisplay:YES];}];
    }
    [fixture.window orderBack:nil]; [fixture publish];
    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:config[@"performance"] ? [config[@"performance"][@"lifetime_ms"] doubleValue]/1000.0 : 90];
    NSTimer *watchdog = [NSTimer scheduledTimerWithTimeInterval:0.05 repeats:YES block:^(NSTimer *timer) {
      NSString *stop = [NSString stringWithContentsOfFile:stopPath encoding:NSUTF8StringEncoding error:nil];
      NSString *human = [NSString stringWithContentsOfFile:[config[@"directory"] stringByAppendingPathComponent:@"human-stage"] encoding:NSUTF8StringEncoding error:nil];
      if (fixture.humanStage == 0 && [human isEqual:config[@"nonce"]]) { fixture.humanStage = 1; fixture.text.stringValue = @"Multica scripted human handoff"; }
      [fixture publish];
      if (fixture.writeFailed || deadline.timeIntervalSinceNow <= 0 || [stop isEqual:config[@"nonce"]] || ![processStart([config[@"owner_pid"] intValue]) isEqual:config[@"owner_start"]]) { [timer invalidate]; [fixture finish]; }
    }];
    [NSApp run]; [animation invalidate]; [watchdog invalidate]; [NSEvent removeMonitor:monitor];
    if (!fixture.closed) { fixture.closed = YES; [fixture.window close]; [fixture publish]; }
    return fixture.writeFailed ? 1 : 0;
  }
}
