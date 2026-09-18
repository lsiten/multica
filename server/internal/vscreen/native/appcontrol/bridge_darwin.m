// go:build darwin && cgo

#import "native_internal.h"

@implementation ACRequest
@end
@implementation ACWindow
@end
@implementation ACPressed
@end
@implementation ACSession
@end

double ACNow(void) {
  struct timespec t;
  clock_gettime(CLOCK_MONOTONIC, &t);
  return t.tv_sec + t.tv_nsec / 1e9;
}
BOOL ACExpired(ACRequest *r) { return r.cancelled || ACNow() >= r.deadline; }
uintptr_t ac_request_new(double seconds) {
  ACRequest *r = [ACRequest new];
  r.deadline = ACNow() + MAX(0, MIN(seconds, 3));
  return (uintptr_t)CFBridgingRetain(r);
}
void ac_request_cancel(uintptr_t handle) {
  ACRequest *r = (__bridge ACRequest *)(void *)handle;
  r.cancelled = YES;
}
void ac_request_free(uintptr_t handle) { CFBridgingRelease((void *)handle); }
uintptr_t ac_new(void) {
  @autoreleasepool {
    ACSession *s = [ACSession new];
    s.windows = [NSMutableDictionary new];
    s.blockedPIDs = [NSMutableSet new];
    s.lock = [NSLock new];
    s.pending = dispatch_group_create();
    s.pressed = [NSMutableDictionary new];
    s.uncertainInput = [NSMutableDictionary new];
    __weak ACSession *weak = s;
    NSNotificationCenter *center =
        NSWorkspace.sharedWorkspace.notificationCenter;
    s.activationObserver = [center
        addObserverForName:NSWorkspaceDidActivateApplicationNotification
                    object:nil
                     queue:nil
                usingBlock:^(NSNotification *n) {
                  ACSession *strong = weak;
                  NSRunningApplication *app =
                      n.userInfo[NSWorkspaceApplicationKey];
                  [strong.lock lock];
                  [strong.blockedPIDs addObject:@(app.processIdentifier)];
                  [strong.lock unlock];
                }];
    s.sessionObserver =
        [center addObserverForName:NSWorkspaceSessionDidResignActiveNotification
                            object:nil
                             queue:nil
                        usingBlock:^(NSNotification *n) {
                          ACSession *strong = weak;
                          [strong.lock lock];
                          strong.sessionBlocked = YES;
                          strong.generation++;
                          [strong.lock unlock];
                        }];
    void (^freezeSession)(NSNotification *) = ^(NSNotification *notification) {
      ACSession *strong = weak;
      [strong.lock lock];
      strong.sessionBlocked = YES;
      strong.generation++;
      [strong.lock unlock];
    };
    s.sleepObserver =
        [center addObserverForName:NSWorkspaceScreensDidSleepNotification
                            object:nil
                             queue:nil
                        usingBlock:freezeSession];
    s.lockObserver = [NSDistributedNotificationCenter.defaultCenter
        addObserverForName:@"com.apple.screenIsLocked"
                    object:nil
                     queue:nil
                usingBlock:freezeSession];
    return (uintptr_t)CFBridgingRetain(s);
  }
}
void ac_free(uintptr_t handle) {
  ACSession *s = CFBridgingRelease((void *)handle);
  NSNotificationCenter *center = NSWorkspace.sharedWorkspace.notificationCenter;
  [center removeObserver:s.activationObserver];
  [center removeObserver:s.sessionObserver];
  [center removeObserver:s.sleepObserver];
  [NSDistributedNotificationCenter.defaultCenter removeObserver:s.lockObserver];
}
int ac_call(uintptr_t handle, uintptr_t request, const char *bytes,
            size_t length, char **out, size_t *size) {
  @autoreleasepool {
    ACSession *s = (__bridge ACSession *)(void *)handle;
    ACRequest *r = (__bridge ACRequest *)(void *)request;
    if (!s || !r || length > 128 * 1024)
      return 1;
    NSDictionary *message = [NSJSONSerialization
        JSONObjectWithData:[NSData dataWithBytes:bytes length:length]
                   options:0
                     error:nil];
    if (![message isKindOfClass:NSDictionary.class])
      return 1;
    NSString *op = message[@"Operation"], *error = nil;
    NSDictionary *input = message[@"Input"], *value = @{};
    if (ACExpired(r))
      error = @"action_uncertain";
    else if ([op isEqual:@"probe"])
      value = @{
        @"Accessibility" : [NSNumber numberWithBool:AXIsProcessTrusted()],
        @"ScreenRecording" :
            [NSNumber numberWithBool:CGPreflightScreenCaptureAccess()]
      };
    else if ([op isEqual:@"list_apps"])
      value = ACListApps(r, &error);
    else if ([op isEqual:@"quiesce"]) {
      if (dispatch_group_wait(
              s.pending, dispatch_time(DISPATCH_TIME_NOW,
                                       (int64_t)(MAX(0, r.deadline - ACNow()) *
                                                 NSEC_PER_SEC))) != 0)
        error = @"action_uncertain";
      if (!error)
        error = ACInputQuiescent(s, [input isKindOfClass:NSDictionary.class]
                                        ? input[@"Resource"]
                                        : nil);
    } else if ([op isEqual:@"forget"]) {
      [s.windows removeObjectForKey:input[@"Window"][@"Handle"]];
    } else if ([op isEqual:@"observe_display"])
      value = ACObserveDisplay(s, r, input, &error);
    else if (!AXIsProcessTrusted())
      error = @"accessibility_denied";
    else if ([op isEqual:@"list_windows"])
      value = ACListWindows(s, r, &error);
    else if ([op isEqual:@"adopt_window"])
      value = ACAdoptWindow(s, r, input, &error);
    else if ([op isEqual:@"resume"]) {
      [s.lock lock];
      NSUInteger generation = s.generation;
      [s.lock unlock];
      NSDictionary *session =
          CFBridgingRelease(CGSessionCopyCurrentDictionary());
      if (![session[(id)kCGSessionOnConsoleKey] boolValue] ||
          ![session[(id)kCGSessionLoginDoneKey] boolValue] ||
          [session[(id)kCGSessionUserIDKey] unsignedIntValue] != getuid() ||
          [session[@"CGSSessionScreenIsLocked"] boolValue])
        error = @"needs_intervention";
      NSMutableArray<ACWindow *> *recovered = [NSMutableArray new];
      for (ACWindow *w in s.windows.allValues) {
        if (error)
          break;
        if (w.displayID != [input[@"ID"] unsignedIntValue])
          continue;
        CGRect bounds;
        error = ACReadWindow(w, &bounds);
        if (error || !ACContains(ACRect(input[@"Bounds"]), bounds) ||
            NSWorkspace.sharedWorkspace.frontmostApplication
                    .processIdentifier == [w.process[@"PID"] intValue]) {
          error = @"needs_intervention";
          break;
        }
        [recovered addObject:w];
      }
      [s.lock lock];
      if (!error && (generation != s.generation || ACExpired(r)))
        error = @"needs_intervention";
      if (!error) {
        s.sessionBlocked = NO;
        for (ACWindow *w in recovered) {
          [s.blockedPIDs removeObject:w.process[@"PID"]];
          w.sessionGeneration = generation;
        }
      }
      [s.lock unlock];
    } else if ([op isEqual:@"launch"]) {
      [s.lock lock];
      BOOL blocked = s.sessionBlocked;
      [s.lock unlock];
      if (blocked)
        error = @"needs_intervention";
      else
        value = ACLaunch(s, r, input, &error);
    } else if ([op isEqual:@"move"])
      value = ACMove(s, r, input, &error);
    else if ([op isEqual:@"restore"])
      value = ACRestore(s, r, input, &error);
    else if ([op isEqual:@"observe"])
      value = ACObserve(s, r, input, &error);
    else if ([op isEqual:@"action"])
      value = ACAction(s, r, input, &error);
    else
      error = @"needs_intervention";
    NSData *encoded = [NSJSONSerialization dataWithJSONObject:@{
      @"Error" : error ?: @"",
      @"Value" : value ?: @{}
    }
                                                      options:0
                                                        error:nil];
    if (!encoded || encoded.length > 12 * 1024 * 1024)
      return 1;
    *size = encoded.length;
    *out = malloc(*size);
    if (!*out)
      return 1;
    memcpy(*out, encoded.bytes, *size);
    return 0;
  }
}
