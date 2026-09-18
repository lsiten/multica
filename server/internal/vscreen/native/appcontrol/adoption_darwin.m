// go:build darwin && cgo

#import "native_internal.h"

static ACWindow *selectionWindow(NSRunningApplication *running, ACRequest *r) {
  NSDictionary *process = ACProcess(running.processIdentifier);
  if (!process || ACExpired(r))
    return nil;
  AXUIElementRef app = AXUIElementCreateApplication(running.processIdentifier);
  NSArray *windows = ACCopy(app, kAXWindowsAttribute, r);
  CFRelease(app);
  // Whole-process input and claims require an unambiguous single window.
  if (![windows isKindOfClass:NSArray.class] || windows.count != 1)
    return nil;
  AXUIElementRef element = (__bridge AXUIElementRef)windows[0];
  CGRect bounds = ACElementBounds(element, r);
  if (CGRectIsEmpty(bounds) || CGRectIsNull(bounds) || CGRectIsInfinite(bounds))
    return nil;
  CGDirectDisplayID displays[2];
  uint32_t count = 0;
  if (CGGetDisplaysWithRect(bounds, 2, displays, &count) != kCGErrorSuccess || count != 1 ||
      !ACContains(CGDisplayBounds(displays[0]), bounds) ||
      (CGDisplayVendorNumber(displays[0]) == 0x4D55 && CGDisplayModelNumber(displays[0]) == 1))
    return nil;
  NSArray *list = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionAll, kCGNullWindowID));
  uint32_t identifier = 0;
  for (NSDictionary *entry in list) {
    CGRect current;
    if ([entry[(id)kCGWindowOwnerPID] intValue] != running.processIdentifier ||
        [entry[(id)kCGWindowLayer] intValue] != 0 ||
        !CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)entry[(id)kCGWindowBounds], &current) ||
        !CGRectEqualToRect(current, bounds))
      continue;
    if (identifier)
      return nil;
    identifier = [entry[(id)kCGWindowNumber] unsignedIntValue];
  }
  if (!identifier || ACExpired(r))
    return nil;
  ACWindow *w = [ACWindow new];
  w.process = process;
  w.element = windows[0];
  w.handle = NSUUID.UUID.UUIDString;
  w.windowID = identifier;
  w.displayID = displays[0];
  w.original = bounds;
  w.lastBounds = bounds;
  w.elements = [NSMutableDictionary new];
  return w;
}

NSDictionary *ACListWindows(ACSession *s, ACRequest *r, NSString **error) {
  NSMutableArray *result = [NSMutableArray new];
  BOOL truncated = NO;
  for (NSRunningApplication *app in NSWorkspace.sharedWorkspace.runningApplications) {
    if (ACExpired(r) || result.count >= 64) {
      truncated = YES;
      break;
    }
    if (app.activationPolicy != NSApplicationActivationPolicyRegular)
      continue;
    BOOL owned = NO;
    for (ACWindow *current in s.windows.allValues)
      if ([current.process[@"PID"] intValue] == app.processIdentifier) {
        owned = YES;
        break;
      }
    if (owned)
      continue;
    ACWindow *w = selectionWindow(app, r);
    if (!w)
      continue;
    id title = ACCopy((__bridge AXUIElementRef)w.element, kAXTitleAttribute, r);
    if (![title isKindOfClass:NSString.class])
      continue;
    NSString *bounded = title;
    while ([bounded lengthOfBytesUsingEncoding:NSUTF8StringEncoding] > 256 && bounded.length)
      bounded = [bounded substringToIndex:[bounded rangeOfComposedCharacterSequenceAtIndex:bounded.length - 1].location];
    [result addObject:@{ @"Window": ACWindowValue(w), @"Title": bounded }];
  }
  return @{ @"Windows": result, @"Truncated": @(truncated) };
}

NSDictionary *ACAdoptWindow(ACSession *s, ACRequest *r, NSDictionary *input, NSString **error) {
  NSDictionary *expected = input[@"Window"];
  NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:[expected[@"Process"][@"PID"] intValue]];
  ACWindow *w = app ? selectionWindow(app, r) : nil;
  if (!w || ![w.process isEqual:expected[@"Process"]] ||
      w.windowID != [expected[@"WindowID"] unsignedIntValue] ||
      w.displayID != [expected[@"DisplayID"] unsignedIntValue] ||
      !CGRectEqualToRect(w.lastBounds, ACRect(expected[@"Bounds"])) ||
      s.windows[expected[@"Handle"]] || s.windows.count >= 128 || ACExpired(r)) {
    *error = @"stale_window";
    return nil;
  }
  for (ACWindow *owned in s.windows.allValues)
    if ([owned.process isEqual:w.process]) {
      *error = @"app_claim_conflict";
      return nil;
    }
  w.handle = expected[@"Handle"];
  w.resource = input[@"Display"][@"Resource"];
  [s.lock lock];
  w.sessionGeneration = s.generation;
  [s.lock unlock];
  s.windows[w.handle] = w;
  return ACWindowValue(w);
}
