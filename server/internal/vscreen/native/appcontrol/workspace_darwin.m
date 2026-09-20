// go:build darwin && cgo

#import "native_internal.h"

static void windowNotice(AXObserverRef observer, AXUIElementRef element,
                         CFStringRef notification, void *context) {}
static uint32_t windowID(pid_t pid, CGRect bounds) {
  NSArray *list = CFBridgingRelease(
      CGWindowListCopyWindowInfo(kCGWindowListOptionAll, kCGNullWindowID));
  uint32_t found = 0;
  for (NSDictionary *entry in list) {
    if ([entry[(id)kCGWindowOwnerPID] intValue] != pid ||
        [entry[(id)kCGWindowLayer] intValue] != 0)
      continue;
    CGRect current;
    if (!CGRectMakeWithDictionaryRepresentation(
            (__bridge CFDictionaryRef)entry[(id)kCGWindowBounds], &current) ||
        !CGRectEqualToRect(current, bounds))
      continue;
    if (found)
      return 0;
    found = [entry[(id)kCGWindowNumber] unsignedIntValue];
  }
  return found;
}
static NSArray<NSURL *> *ACRequestedFiles(NSDictionary *input, NSString **error) {
  id rawFiles = input[@"Files"];
  NSArray *requestedFiles = @[];
  if (rawFiles && rawFiles != NSNull.null) {
    if (![rawFiles isKindOfClass:NSArray.class]) { *error = @"invalid_launch"; return nil; }
    requestedFiles = rawFiles;
  }
  NSMutableArray<NSURL *> *files = [NSMutableArray new];
  for (id value in requestedFiles) {
    if (![value isKindOfClass:NSString.class]) { *error = @"invalid_launch"; return nil; }
    NSString *path = value; BOOL directory = NO;
    if (!path.isAbsolutePath || ![NSFileManager.defaultManager fileExistsAtPath:path isDirectory:&directory] || directory) { *error = @"invalid_launch"; return nil; }
    [files addObject:[NSURL fileURLWithPath:path]];
  }
  return files;
}

NSDictionary *ACLaunch(ACSession *s, ACRequest *r, NSDictionary *input,
                       NSString **error) {
  if (s.windows.count >= 128) {
    *error = @"needs_intervention";
    return nil;
  }
  NSString *bundle = input[@"BundleID"];
  NSURL *url = [NSWorkspace.sharedWorkspace
      URLForApplicationWithBundleIdentifier:bundle];
  if (!url || ![[NSBundle bundleWithURL:url].bundleIdentifier isEqual:bundle]) {
    *error = @"invalid_launch";
    return nil;
  }
  // Running instances are refused: launch cannot prove a new window belongs to
  // this runtime.
  if ([NSRunningApplication runningApplicationsWithBundleIdentifier:bundle]
          .count) {
    *error = @"needs_intervention";
    return nil;
  }
  NSArray<NSURL *> *files = ACRequestedFiles(input, error);
  if (!files) return nil;
  dispatch_semaphore_t completed = dispatch_semaphore_create(0);
  __block NSRunningApplication *launched = nil;
  dispatch_group_enter(s.pending);
  dispatch_async(dispatch_get_main_queue(), ^{
    if (ACExpired(r)) {
      dispatch_group_leave(s.pending);
      dispatch_semaphore_signal(completed);
      return;
    }
    NSWorkspaceOpenConfiguration *config =
        NSWorkspaceOpenConfiguration.configuration;
    config.activates = NO;
    config.createsNewApplicationInstance = NO;
    config.allowsRunningApplicationSubstitution = NO;
    config.promptsUserIfNeeded = NO;
    config.addsToRecentItems = NO;
    void (^done)(NSRunningApplication *, NSError *) =
        ^(NSRunningApplication *app, NSError *e) {
          launched = e ? nil : app;
          dispatch_group_leave(s.pending);
          dispatch_semaphore_signal(completed);
        };
    if (files.count)
      [NSWorkspace.sharedWorkspace openURLs:files
                       withApplicationAtURL:url
                              configuration:config
                          completionHandler:done];
    else
      [NSWorkspace.sharedWorkspace openApplicationAtURL:url
                                          configuration:config
                                      completionHandler:done];
  });
  if (dispatch_semaphore_wait(
          completed, dispatch_time(DISPATCH_TIME_NOW,
                                   (int64_t)(MAX(0, r.deadline - ACNow()) *
                                             NSEC_PER_SEC))) != 0 ||
      ACExpired(r)) {
    *error = @"action_uncertain";
    return nil;
  }
  if (!launched) {
    *error = @"native_unavailable";
    return nil;
  }
  NSDictionary *process = ACProcess(launched.processIdentifier);
  if (!process) {
    *error = @"stale_window";
    return nil;
  }
  AXUIElementRef app = AXUIElementCreateApplication(launched.processIdentifier);
  AXObserverRef observer = NULL;
  if (AXObserverCreate(launched.processIdentifier, windowNotice, &observer) !=
      kAXErrorSuccess) {
    CFRelease(app);
    *error = @"needs_intervention";
    return nil;
  }
  AXError registered = AXObserverAddNotification(
      observer, app, kAXWindowCreatedNotification, NULL);
  if (registered != kAXErrorSuccess) {
    CFRelease(observer);
    CFRelease(app);
    *error = @"needs_intervention";
    return nil;
  }
  CFRunLoopAddSource(CFRunLoopGetCurrent(),
                     AXObserverGetRunLoopSource(observer),
                     kCFRunLoopDefaultMode);
  NSArray *windows = nil;
  while (!ACExpired(r)) {
    windows = ACCopy(app, kAXWindowsAttribute, r);
    if ([windows isKindOfClass:NSArray.class] && windows.count)
      break;
    CFRunLoopRunInMode(kCFRunLoopDefaultMode,
                       MIN(.05, MAX(0, r.deadline - ACNow())), true);
  }
  AXObserverRemoveNotification(observer, app, kAXWindowCreatedNotification);
  CFRunLoopRemoveSource(CFRunLoopGetCurrent(),
                        AXObserverGetRunLoopSource(observer),
                        kCFRunLoopDefaultMode);
  CFRelease(observer);
  CFRelease(app);
  if (windows.count != 1 || ACExpired(r)) {
    *error = @"needs_intervention";
    return nil;
  }
  AXUIElementRef element = (__bridge AXUIElementRef)windows[0];
  CGRect bounds = ACElementBounds(element, r);
  uint32_t identifier = windowID(launched.processIdentifier, bounds);
  if (!identifier) {
    *error = @"stale_window";
    return nil;
  }
  ACWindow *w = [ACWindow new];
  w.process = process;
  [s.lock lock];
  w.sessionGeneration = s.generation;
  [s.lock unlock];
  w.element = windows[0];
  w.handle = NSUUID.UUID.UUIDString;
  w.windowID = identifier;
  w.original = bounds;
  w.lastBounds = bounds;
  w.elements = [NSMutableDictionary new];
  s.windows[w.handle] = w;
  return ACWindowValue(w);
}
NSDictionary *ACMove(ACSession *s, ACRequest *r, NSDictionary *input,
                     NSString **error) {
  ACWindow *w = s.windows[input[@"Window"][@"Handle"]];
  NSDictionary *d = input[@"Display"];
  BOOL background = [input[@"Background"] boolValue];
  *error = ACGuard(s, w, d, r, background);
  if (*error)
    return nil;
  CGRect current;
  *error = ACReadWindow(w, &current);
  if (*error)
    return nil;
  if (background && !CGRectEqualToRect(current, w.lastBounds)) {
    *error = @"needs_intervention";
    return nil;
  }
  CGRect display = ACRect(d[@"Bounds"]);
  CGSize size = CGSizeMake(MIN(current.size.width, display.size.width),
                           MIN(current.size.height, display.size.height));
  CGPoint point =
      CGPointMake(display.origin.x + (display.size.width - size.width) / 2,
                  display.origin.y + (display.size.height - size.height) / 2);
  if ([input[@"Restore"] boolValue]) {
    point = w.original.origin;
    size = w.original.size;
    if (!ACContains(display, (CGRect){point, size})) {
      size = CGSizeMake(MIN(size.width, display.size.width),
                        MIN(size.height, display.size.height));
      point = display.origin;
    }
  }
  AXUIElementRef element = (__bridge AXUIElementRef)w.element;
  Boolean positionSettable = false, sizeSettable = false;
  if (AXUIElementIsAttributeSettable(element, kAXPositionAttribute,
                                     &positionSettable) != kAXErrorSuccess ||
      !positionSettable ||
      AXUIElementIsAttributeSettable(element, kAXSizeAttribute,
                                     &sizeSettable) != kAXErrorSuccess ||
      !sizeSettable) {
    *error = @"needs_intervention";
    return nil;
  }
  if (ACExpired(r)) {
    *error = @"action_uncertain";
    return nil;
  }
  if (d[@"Resource"])
    w.resource = d[@"Resource"];
  AXUIElementSetMessagingTimeout(
      element, (float)MAX(.001, MIN(.1, r.deadline - ACNow())));
  AXValueRef value = AXValueCreate(kAXValueCGSizeType, &size);
  AXError status =
      AXUIElementSetAttributeValue(element, kAXSizeAttribute, value);
  CFRelease(value);
  if (status != kAXErrorSuccess || ACExpired(r)) {
    if (status != kAXErrorSuccess)
      ACMarkUncertain(s, w, @"ax_move");
    *error = @"action_uncertain";
    return nil;
  }
  *error = ACGuard(s, w, d, r, background);
  if (*error)
    return nil;
  value = AXValueCreate(kAXValueCGPointType, &point);
  status = AXUIElementSetAttributeValue(element, kAXPositionAttribute, value);
  CFRelease(value);
  if (status != kAXErrorSuccess || ACExpired(r)) {
    if (status != kAXErrorSuccess)
      ACMarkUncertain(s, w, @"ax_move");
    *error = @"action_uncertain";
    return nil;
  }
  CGRect readback = ACElementBounds(element, r);
  if (!CGRectEqualToRect(readback, (CGRect){point, size})) {
    *error = @"action_uncertain";
    return nil;
  }
  w.lastBounds = readback;
  w.displayID = [d[@"ID"] unsignedIntValue];
  if (d[@"Resource"])
    w.resource = d[@"Resource"];
  w.moved = YES;
  w.snapshotValid = NO;
  [w.elements removeAllObjects];
  if ([input[@"Activate"] boolValue]) {
    // Only the separately authorized local human-transfer path sets Activate.
    if (ACExpired(r)) {
      *error = @"action_uncertain";
      return nil;
    }
    if (![ACProcess([w.process[@"PID"] intValue]) isEqual:w.process]) {
      *error = @"stale_window";
      return nil;
    }
    [[NSRunningApplication
        runningApplicationWithProcessIdentifier:[w.process[@"PID"] intValue]]
        activateWithOptions:0];
  }
  return ACWindowValue(w);
}

NSDictionary *ACRestore(ACSession *s, ACRequest *r, NSDictionary *input,
                        NSString **error) {
  ACWindow *w = s.windows[input[@"Window"][@"Handle"]];
  if (!w) {
    *error = @"stale_window";
    return nil;
  }
  if (!w.moved || ACProcessEnded(w.process))
    return @{};
  CGRect current;
  *error = ACReadWindow(w, &current);
  if (*error)
    return nil;
  // A user-moved/resized window is no longer an automatic cleanup target.
  if (!CGRectEqualToRect(current, w.lastBounds)) {
    *error = @"needs_intervention";
    return nil;
  }
  CGDirectDisplayID displays[128];
  uint32_t count = 0;
  CGDirectDisplayID chosen = 0;
  if (CGGetActiveDisplayList(128, displays, &count) != kCGErrorSuccess) {
    *error = @"source_gone";
    return nil;
  }
  for (uint32_t i = 0; i < count; i++) {
    if (CGDisplayVendorNumber(displays[i]) == 0x4D55 &&
        CGDisplayModelNumber(displays[i]) == 1)
      continue;
    if (!chosen)
      chosen = displays[i];
    if (ACContains(CGDisplayBounds(displays[i]), w.original)) {
      chosen = displays[i];
      break;
    }
  }
  if (!chosen) {
    *error = @"source_gone";
    return nil;
  }
  NSDictionary *display = @{
    @"ID" : @(chosen),
    @"Bounds" : ACBounds(CGDisplayBounds(chosen)),
    @"Virtual" : @NO
  };
  return ACMove(
      s, r, @{
        @"Window" : ACWindowValue(w),
        @"Display" : display,
        @"Restore" : @YES,
        @"Background" : @YES
      },
      error);
}
