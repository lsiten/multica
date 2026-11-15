//go:build darwin && cgo

#import "display.h"
#import <AppKit/AppKit.h>
#import <CoreGraphics/CoreGraphics.h>

static int readDisplay(uint32_t display, MMRDisplay *out) {
  if (!CGDisplayIsOnline(display))
    return 1;
  memset(out, 0, sizeof(*out));
  out->id = display;
  out->main = display == CGMainDisplayID();
  out->builtin = CGDisplayIsBuiltin(display) == 1;
  CGRect bounds = CGDisplayBounds(display);
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

int mmr_supported(void) { return 1; }

int mmr_list(MMRDisplay *out, uint32_t capacity, uint32_t *count) {
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
    for (uint32_t index = 0; index < enumerated; index++) {
      int status = readDisplay(ids[index], &out[*count]);
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

void mmr_run(void) {
  @autoreleasepool {
    [NSApplication sharedApplication];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyProhibited];
    [NSApp run];
  }
}

void mmr_stop(void) {
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
