//go:build darwin && cgo

#import "native_internal.h"

NSDictionary *ACManagedWindows(ACSession *s, ACRequest *r, NSDictionary *display, NSString **error) {
  NSMutableArray<NSString *> *handles = [NSMutableArray new];
  NSArray *live = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionAll, kCGNullWindowID));
  if (!live) { *error = @"native_unavailable"; return nil; }
  for (NSString *handle in s.windows) {
    if (ACExpired(r)) { *error = @"action_uncertain"; return nil; }
    ACWindow *window = s.windows[handle];
    if (window.displayID != [display[@"ID"] unsignedIntValue] || ACProcessEnded(window.process)) continue;
    for (NSDictionary *entry in live) {
      if ([entry[(id)kCGWindowNumber] unsignedIntValue] == window.windowID && [entry[(id)kCGWindowOwnerPID] intValue] == [window.process[@"PID"] intValue]) {
        CGRect bounds;
        if (!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)entry[(id)kCGWindowBounds], &bounds) || !ACContains(ACRect(display[@"Bounds"]),bounds)) break;
        [handles addObject:handle];
        break;
      }
    }
  }
  return @{@"Handles":handles};
}
