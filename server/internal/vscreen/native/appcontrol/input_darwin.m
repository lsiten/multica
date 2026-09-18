// go:build darwin && cgo

#import "native_internal.h"

static CGEventRef privateKey(CGKeyCode code, bool down) {
  CGEventSourceRef source = CGEventSourceCreate(kCGEventSourceStatePrivate);
  if (!source)
    return NULL;
  CGEventRef event = CGEventCreateKeyboardEvent(source, code, down);
  CFRelease(source);
  return event;
}
static CGEventRef privateMouse(CGEventType type, CGPoint point,
                               CGMouseButton button) {
  CGEventSourceRef source = CGEventSourceCreate(kCGEventSourceStatePrivate);
  if (!source)
    return NULL;
  CGEventRef event = CGEventCreateMouseEvent(source, type, point, button);
  CFRelease(source);
  return event;
}
static CGEventRef privateScroll(int32_t y, int32_t x) {
  CGEventSourceRef source = CGEventSourceCreate(kCGEventSourceStatePrivate);
  if (!source)
    return NULL;
  CGEventRef event =
      CGEventCreateScrollWheelEvent(source, kCGScrollEventUnitPixel, 2, y, x);
  CFRelease(source);
  return event;
}
static BOOL framePoint(ACWindow *w, NSDictionary *raw, CGPoint *point) {
  if (!raw || !w.frameWidth || !w.frameHeight)
    return NO;
  double x = [raw[@"x"] doubleValue], y = [raw[@"y"] doubleValue];
  if (!isfinite(x) || !isfinite(y) || x < 0 || y < 0 || x >= w.frameWidth ||
      y >= w.frameHeight)
    return NO;
  *point = CGPointMake(
      w.lastBounds.origin.x + x * w.lastBounds.size.width / w.frameWidth,
      w.lastBounds.origin.y + y * w.lastBounds.size.height / w.frameHeight);
  return YES;
}
static NSString *pressKey(pid_t pid, CGEventRef event) {
  CGEventType type = CGEventGetType(event);
  if (type == kCGEventKeyDown || type == kCGEventKeyUp)
    return [NSString stringWithFormat:@"%d/key/%lld", pid,
                                      CGEventGetIntegerValueField(
                                          event, kCGKeyboardEventKeycode)];
  if (type == kCGEventLeftMouseDown || type == kCGEventLeftMouseUp)
    return [NSString stringWithFormat:@"%d/mouse/left", pid];
  return nil;
}
static void releaseInterrupted(ACSession *s, ACWindow *w) {
  for (NSString *key in s.pressed.allKeys) {
    ACPressed *pressed = s.pressed[key];
    if (![pressed.process isEqual:w.process])
      continue;
    pid_t pid = [pressed.process[@"PID"] intValue];
    if (!ACProcessEnded(pressed.process)) {
      if ([ACProcess(pid) isEqual:pressed.process] &&
          CGPreflightPostEventAccess())
        CGEventPostToPid(pid, (__bridge CGEventRef)pressed.releaseEvent);
      // Posting has no delivery acknowledgement. Do not call an attempted
      // release quiescence.
      s.uncertainInput[key] = @{
        @"Process" : pressed.process,
        @"Resource" : pressed.resource ?: @{}
      };
    }
    [s.pressed removeObjectForKey:key];
  }
}
void ACMarkUncertain(ACSession *s, ACWindow *w, NSString *operation) {
  NSString *key = [NSString
      stringWithFormat:@"%@/%@/%@", w.process[@"PID"], w.handle, operation];
  s.uncertainInput[key] =
      @{@"Process" : w.process,
        @"Resource" : w.resource ?: @{}};
}
NSString *ACInputQuiescent(ACSession *s, NSDictionary *resource) {
  BOOL blocked = NO;
  for (NSString *key in s.uncertainInput.allKeys) {
    NSDictionary *entry = s.uncertainInput[key], *process = entry[@"Process"],
                 *owner = entry[@"Resource"];
    if (ACProcessEnded(process)) {
      [s.uncertainInput removeObjectForKey:key];
      continue;
    }
    if (!resource || !owner.count || [resource isEqual:owner])
      blocked = YES;
  }
  for (ACPressed *pressed in s.pressed.allValues) {
    if (!resource || !pressed.resource.count ||
        [resource isEqual:pressed.resource])
      blocked = YES;
  }
  return blocked ? @"action_uncertain" : nil;
}

static NSString *post(ACSession *s, ACRequest *r, ACWindow *w, NSDictionary *d,
                      CGEventRef event) {
  if (!event)
    return @"native_unavailable";
  NSString *error = ACGuard(s, w, d, r, YES);
  CGRect bounds;
  if (!error)
    error = ACReadWindow(w, &bounds);
  if (!error && (!CGRectEqualToRect(bounds, w.lastBounds) ||
                 !ACContains(ACRect(d[@"Bounds"]), bounds)))
    error = @"stale_window";
  if (!error && ACExpired(r))
    error = @"action_uncertain";
  if (!error) {
    pid_t pid = [w.process[@"PID"] intValue];
    NSString *key = pressKey(pid, event);
    CGEventType type = CGEventGetType(event);
    if (type == kCGEventKeyDown || type == kCGEventLeftMouseDown) {
      CGEventRef up = CGEventCreateCopy(event);
      if (!up) {
        CFRelease(event);
        return @"native_unavailable";
      }
      CGEventSetType(up, type == kCGEventKeyDown ? kCGEventKeyUp
                                                 : kCGEventLeftMouseUp);
      CGEventSetFlags(up, 0);
      ACPressed *pressed = [ACPressed new];
      pressed.process = w.process;
      pressed.resource = w.resource;
      pressed.releaseEvent = CFBridgingRelease(up);
      s.pressed[key] = pressed;
    }
    CGEventPostToPid(pid, event);
    if (type == kCGEventKeyUp || type == kCGEventLeftMouseUp)
      [s.pressed removeObjectForKey:key];
  }
  CFRelease(event);
  return error;
}
static CGEventFlags flags(NSArray *modifiers) {
  CGEventFlags f = 0;
  for (NSString *m in modifiers) {
    if ([m isEqual:@"shift"])
      f |= kCGEventFlagMaskShift;
    else if ([m isEqual:@"control"])
      f |= kCGEventFlagMaskControl;
    else if ([m isEqual:@"alt"])
      f |= kCGEventFlagMaskAlternate;
    else if ([m isEqual:@"meta"])
      f |= kCGEventFlagMaskCommand;
  }
  return f;
}
static NSString *dispatchInput(ACSession *s, ACRequest *r, ACWindow *w,
                               NSDictionary *d, NSDictionary *action) {
  // This function is unreachable without the host's reviewed app/OS/action
  // capability. Events target only this PID; no global event tap, cursor warp
  // or clipboard is used.
  if (!CGPreflightPostEventAccess())
    return @"accessibility_denied";
  NSString *kind = action[@"kind"], *error = nil;
  if ([kind isEqual:@"key"] || [kind isEqual:@"type"]) {
    BOOL unicode = [kind isEqual:@"type"];
    NSNumber *code = @0;
    CGEventFlags modifiers = 0;
    NSString *text = @"";
    if (unicode) {
      text = action[@"type"][@"text"];
      if (text.length > 8192)
        return @"needs_intervention";
    } else {
      NSDictionary *codes = @{
        @"Enter" : @36,
        @"Return" : @36,
        @"Tab" : @48,
        @"Space" : @49,
        @"Escape" : @53,
        @"Backspace" : @51,
        @"Delete" : @117,
        @"ArrowLeft" : @123,
        @"ArrowRight" : @124,
        @"ArrowDown" : @125,
        @"ArrowUp" : @126
      };
      NSString *name = action[@"key"][@"key"];
      code = codes[name];
      if (!code) {
        NSDictionary *ansi = @{
          @"A" : @0,
          @"S" : @1,
          @"D" : @2,
          @"F" : @3,
          @"H" : @4,
          @"G" : @5,
          @"Z" : @6,
          @"X" : @7,
          @"C" : @8,
          @"V" : @9,
          @"B" : @11,
          @"Q" : @12,
          @"W" : @13,
          @"E" : @14,
          @"R" : @15,
          @"Y" : @16,
          @"T" : @17,
          @"1" : @18,
          @"2" : @19,
          @"3" : @20,
          @"4" : @21,
          @"6" : @22,
          @"5" : @23,
          @"9" : @25,
          @"7" : @26,
          @"8" : @28,
          @"0" : @29,
          @"O" : @31,
          @"U" : @32,
          @"I" : @34,
          @"P" : @35,
          @"L" : @37,
          @"J" : @38,
          @"K" : @40,
          @"N" : @45,
          @"M" : @46
        };
        code = ansi[name.uppercaseString];
      }
      if (!code)
        return @"needs_intervention";
      modifiers = flags(action[@"key"][@"modifiers"]);
    }
    for (int down = 1; down >= 0; down--) {
      CGEventRef event = privateKey(code.unsignedShortValue, down);
      if (!event)
        return @"native_unavailable";
      CGEventSetFlags(event, modifiers);
      if (unicode) {
        UniChar characters[8192];
        [text getCharacters:characters range:NSMakeRange(0, text.length)];
        CGEventKeyboardSetUnicodeString(event, text.length, characters);
      }
      error = post(s, r, w, d, event);
      if (error)
        return error;
    }
  } else if ([kind isEqual:@"scroll"]) {
    NSDictionary *scroll = action[@"scroll"];
    CGPoint point;
    if (!framePoint(w, scroll[@"position"], &point))
      return @"stale_window";
    CGEventRef event = privateScroll([scroll[@"delta_y"] intValue],
                                     [scroll[@"delta_x"] intValue]);
    if (event)
      CGEventSetLocation(event, point);
    error = post(s, r, w, d, event);
  } else if ([kind isEqual:@"click"]) {
    CGPoint point;
    if (!framePoint(w, action[@"click"][@"position"], &point))
      return @"needs_intervention";
    error =
        post(s, r, w, d,
             privateMouse(kCGEventLeftMouseDown, point, kCGMouseButtonLeft));
    if (!error)
      error =
          post(s, r, w, d,
               privateMouse(kCGEventLeftMouseUp, point, kCGMouseButtonLeft));
  } else if ([kind isEqual:@"drag"]) {
    NSDictionary *drag = action[@"drag"];
    CGPoint from, to;
    if (!framePoint(w, drag[@"from"], &from) ||
        !framePoint(w, drag[@"to"], &to))
      return @"stale_window";
    double seconds = [drag[@"duration_ms"] doubleValue] / 1000.;
    if (seconds <= 0 || seconds > 3)
      return @"needs_intervention";
    error = post(s, r, w, d,
                 privateMouse(kCGEventLeftMouseDown, from, kCGMouseButtonLeft));
    if (error)
      return error;
    double start = ACNow();
    for (int step = 1; step <= 16; step++) {
      double target = start + seconds * step / 16.;
      while (ACNow() < target && !ACExpired(r)) {
        struct timespec pause = {0, 1000000};
        nanosleep(&pause, NULL);
      }
      CGPoint point = CGPointMake(from.x + (to.x - from.x) * step / 16.,
                                  from.y + (to.y - from.y) * step / 16.);
      error = post(
          s, r, w, d,
          privateMouse(kCGEventLeftMouseDragged, point, kCGMouseButtonLeft));
      if (error)
        return error;
    }
    error = post(s, r, w, d,
                 privateMouse(kCGEventLeftMouseUp, to, kCGMouseButtonLeft));
  } else
    return @"needs_intervention";
  return error;
}

NSString *ACPIDAction(ACSession *s, ACRequest *r, ACWindow *w, NSDictionary *d,
                      NSDictionary *action) {
  NSString *error = dispatchInput(s, r, w, d, action);
  if (error)
    releaseInterrupted(s, w);
  return error;
}
