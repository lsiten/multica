//go:build darwin && cgo

#import "display.h"
#import <AppKit/AppKit.h>
#import <CoreGraphics/CoreGraphics.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <objc/runtime.h>
#include <time.h>

// Private ABI declarations are resolved by class name and selector at runtime.
@protocol VSPrivateDisplay <NSObject>
- (instancetype)initWithWidth:(uint32_t)width
                       height:(uint32_t)height
                  refreshRate:(double)rate;
- (instancetype)initWithDescriptor:(id)descriptor;
- (BOOL)applySettings:(id)settings;
- (uint32_t)displayID;
- (void)setQueue:(dispatch_queue_t)queue;
- (void)setName:(NSString *)name;
- (void)setSizeInMillimeters:(CGSize)size;
- (void)setMaxPixelsWide:(uint32_t)value;
- (void)setMaxPixelsHigh:(uint32_t)value;
- (void)setSerialNum:(uint32_t)value;
- (void)setProductID:(uint32_t)value;
- (void)setVendorID:(uint32_t)value;
- (void)setRedPrimary:(CGPoint)value;
- (void)setGreenPrimary:(CGPoint)value;
- (void)setBluePrimary:(CGPoint)value;
- (void)setWhitePoint:(CGPoint)value;
- (void)setModes:(NSArray *)modes;
- (void)setHiDPI:(uint32_t)value;
@end

static NSMutableDictionary<NSNumber *, id> *registry;
static BOOL hasSelectors(NSString *name, NSArray<NSString *> *selectors) {
  Class cls = NSClassFromString(name);
  if (!cls)
    return NO;
  for (NSString *selector in selectors)
    if (![cls instancesRespondToSelector:NSSelectorFromString(selector)])
      return NO;
  return YES;
}
int vs_supported(void) {
  return hasSelectors(
             @"CGVirtualDisplay",
             @[ @"initWithDescriptor:", @"applySettings:", @"displayID" ]) &&
         hasSelectors(@"CGVirtualDisplayMode",
                      @[ @"initWithWidth:height:refreshRate:" ]) &&
         hasSelectors(@"CGVirtualDisplaySettings",
                      @[ @"setModes:", @"setHiDPI:" ]) &&
         hasSelectors(@"CGVirtualDisplayDescriptor", @[
           @"setQueue:", @"setName:", @"setSizeInMillimeters:",
           @"setMaxPixelsWide:", @"setMaxPixelsHigh:", @"setSerialNum:",
           @"setProductID:", @"setVendorID:", @"setRedPrimary:",
           @"setGreenPrimary:", @"setBluePrimary:", @"setWhitePoint:"
         ]);
}
static int readDisplay(uint32_t display, VSDisplay *out) {
  if (!CGDisplayIsOnline(display))
    return 1;
  memset(out, 0, sizeof(*out));
  out->id = display;
  out->main = (display == CGMainDisplayID());
  out->mirror = CGDisplayMirrorsDisplay(display);
  CGRect bounds = CGDisplayBounds(display);
  out->builtin = CGDisplayIsBuiltin(display) == 1;
  out->managed = CGDisplayVendorNumber(display) == 0x4D55 &&
                 CGDisplayModelNumber(display) == 1;
  out->logical_width = bounds.size.width;
  out->logical_height = bounds.size.height;
  for (NSScreen *screen in NSScreen.screens) {
    if ([screen.deviceDescription[@"NSScreenNumber"] unsignedIntValue] ==
        display) {
      [screen.localizedName getCString:out->name
                             maxLength:sizeof(out->name)
                              encoding:NSUTF8StringEncoding];
      break;
    }
  }
  out->x = (int32_t)bounds.origin.x;
  out->y = (int32_t)bounds.origin.y;
  out->width = (uint32_t)CGDisplayPixelsWide(display);
  out->height = (uint32_t)CGDisplayPixelsHigh(display);
  out->scale = bounds.size.width > 0 ? out->width / bounds.size.width : 1;
  CFUUIDRef uuid = CGDisplayCreateUUIDFromDisplayID(display);
  if (!uuid)
    return 2;
  CFStringRef string = CFUUIDCreateString(NULL, uuid);
  BOOL converted = CFStringGetCString(string, out->uuid, sizeof(out->uuid),
                                      kCFStringEncodingUTF8);
  CFRelease(string);
  CFRelease(uuid);
  return converted ? 0 : 3;
}
int vs_list(VSDisplay *out, uint32_t capacity, uint32_t *count) {
  __block int result = 0;
  dispatch_sync(dispatch_get_main_queue(), ^{
    uint32_t ids[128];
    uint32_t limit = MIN(capacity, 128);
    if (CGGetOnlineDisplayList(limit, ids, count) != kCGErrorSuccess) {
      result = 1;
      return;
    }
    uint32_t enumerated = *count;
    *count = 0;
    for (uint32_t i = 0; i < enumerated; i++) {
      int status = readDisplay(ids[i], &out[*count]);
      // A display may be removed between enumeration and metadata readback.
      if (status == 1)
        continue;
      if (status != 0) {
        result = 2;
        return;
      }
      (*count)++;
    }
  });
  return result;
}
int vs_describe(uint32_t display, VSDisplay *out) {
  __block int result = 0;
  dispatch_sync(dispatch_get_main_queue(), ^{
    result = readDisplay(display, out);
  });
  if (result)
    return result;
  out->recording = CGPreflightScreenCaptureAccess();
  if (!out->recording)
    return 0;
  if (@available(macOS 12.3, *)) {
    dispatch_semaphore_t semaphore = dispatch_semaphore_create(0);
    __block BOOL found = NO;
    [SCShareableContent
        getShareableContentExcludingDesktopWindows:YES
                               onScreenWindowsOnly:NO
                                 completionHandler:^(
                                     SCShareableContent *content,
                                     NSError *error) {
                                   if (!error)
                                     for (SCDisplay *candidate in content
                                              .displays)
                                       if (candidate.displayID == display)
                                         found = YES;
                                   dispatch_semaphore_signal(semaphore);
                                 }];
    if (dispatch_semaphore_wait(
            semaphore, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC)) == 0)
      out->capture = found;
  }
  return 0;
}
int vs_create(const char *name, uint32_t serial, uint32_t width,
              uint32_t height, VSDisplay *out) {
  if (!vs_supported())
    return 1;
  __block int result = 0;
  __block uint32_t displayID = 0;
  __block CGFloat right = 0;
  dispatch_sync(dispatch_get_main_queue(), ^{
    @autoreleasepool {
      @try {
        uint32_t ids[128], count = 0;
        if (CGGetOnlineDisplayList(128, ids, &count) != kCGErrorSuccess) {
          result = 2;
          return;
        }
        for (uint32_t i = 0; i < count; i++)
          right = MAX(right, CGRectGetMaxX(CGDisplayBounds(ids[i])));
        id<VSPrivateDisplay> descriptor =
            [[NSClassFromString(@"CGVirtualDisplayDescriptor") alloc] init];
        [descriptor setQueue:dispatch_queue_create("ai.multica.vscreen.display",
                                                   DISPATCH_QUEUE_SERIAL)];
        [descriptor setName:[NSString stringWithUTF8String:name]];
        [descriptor setMaxPixelsWide:width];
        [descriptor setMaxPixelsHigh:height];
        [descriptor
            setSizeInMillimeters:CGSizeMake(width * 0.25, height * 0.25)];
        [descriptor setSerialNum:serial];
        [descriptor setVendorID:0x4D55];
        [descriptor setProductID:0x0001];
        [descriptor setRedPrimary:CGPointMake(.64, .33)];
        [descriptor setGreenPrimary:CGPointMake(.30, .60)];
        [descriptor setBluePrimary:CGPointMake(.15, .06)];
        [descriptor setWhitePoint:CGPointMake(.3127, .3290)];
        id<VSPrivateDisplay> display = [[NSClassFromString(@"CGVirtualDisplay")
            alloc] initWithDescriptor:descriptor];
        if (!display) {
          result = 3;
          return;
        }
        id<VSPrivateDisplay> settings =
            [[NSClassFromString(@"CGVirtualDisplaySettings") alloc] init];
        id<VSPrivateDisplay> mode = [[NSClassFromString(@"CGVirtualDisplayMode")
            alloc] initWithWidth:width height:height refreshRate:30.0];
        if (!mode) {
          result = 4;
          return;
        }
        [settings setModes:@[ mode ]];
        [settings setHiDPI:0];
        if (![display applySettings:settings]) {
          result = 5;
          return;
        }
        displayID = [display displayID];
        if (!displayID) {
          result = 6;
          return;
        }
        registry[@(displayID)] = display;
      } @catch (NSException *exception) {
        result = 10;
      }
    }
  });
  if (result)
    return result;
  // Settings become visible asynchronously; configure only after system
  // enumeration sees the display.
  BOOL online = NO;
  struct timespec interval = {0, 10000000};
  for (int attempt = 0; attempt < 500; attempt++) {
    VSDisplay displays[128];
    uint32_t count = 0;
    if (vs_list(displays, 128, &count) == 0)
      for (uint32_t i = 0; i < count; i++)
        if (displays[i].id == displayID)
          online = YES;
    if (online)
      break;
    nanosleep(&interval, NULL);
  }
  if (!online) {
    vs_dispose(displayID);
    return 11;
  }
  dispatch_sync(dispatch_get_main_queue(), ^{
    if (CGDisplayBounds(displayID).origin.x < right) {
      CGDisplayConfigRef config = NULL;
      if (CGBeginDisplayConfiguration(&config) != kCGErrorSuccess) {
        result = 7;
        return;
      }
      if (CGConfigureDisplayOrigin(config, displayID, (int32_t)right + 64, 0) !=
          kCGErrorSuccess) {
        CGCancelDisplayConfiguration(config);
        result = 8;
        return;
      }
      if (CGCompleteDisplayConfiguration(config, kCGConfigureForAppOnly) !=
          kCGErrorSuccess) {
        result = 9;
        return;
      }
    }
  });
  if (result) {
    vs_dispose(displayID);
    return result;
  }
  result = vs_describe(displayID, out);
  if (result)
    vs_dispose(displayID);
  return result;
}
int vs_dispose(uint32_t display) {
  __block int result = 0;
  dispatch_sync(dispatch_get_main_queue(), ^{
    @autoreleasepool {
      if (!registry[@(display)]) {
        result = 1;
        return;
      }
      [registry removeObjectForKey:@(display)];
    }
  });
  if (result)
    return result;
  // Per-ID online checks stay stale after release; poll the fresh system
  // display list.
  struct timespec interval = {0, 10000000};
  for (int attempt = 0; attempt < 500; attempt++) {
    __block BOOL online = YES;
    dispatch_sync(dispatch_get_main_queue(), ^{
      uint32_t ids[128], count = 0;
      if (CGGetOnlineDisplayList(128, ids, &count) == kCGErrorSuccess) {
        online = NO;
        for (uint32_t i = 0; i < count; i++)
          if (ids[i] == display)
            online = YES;
      }
    });
    if (!online)
      return 0;
    nanosleep(&interval, NULL);
  }
  return 2;
}
void vs_run(void) {
  @autoreleasepool {
    [NSApplication sharedApplication];
    registry = [NSMutableDictionary dictionary];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyProhibited];
    [NSApp run];
    [registry removeAllObjects];
    registry = nil;
  }
}
void vs_stop(void) {
  dispatch_async(dispatch_get_main_queue(), ^{
    [NSApp stop:nil];
    NSEvent *event = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
                                        location:NSZeroPoint
                                   modifierFlags:0
                                       timestamp:0
                                    windowNumber:0
                                         context:nil
                                         subtype:0
                                           data1:0
                                           data2:0];
    [NSApp postEvent:event atStart:NO];
  });
}
