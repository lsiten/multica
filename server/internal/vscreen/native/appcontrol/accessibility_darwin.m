// go:build darwin && cgo

#import "native_internal.h"

static NSString *boundedString(id value, NSUInteger limit) {
  if (![value isKindOfClass:NSString.class])
    return @"";
  return [value substringToIndex:MIN([value length], limit)];
}
static BOOL withinWindow(AXUIElementRef element, ACWindow *w, ACRequest *r) {
  AXUIElementRef root = (__bridge AXUIElementRef)w.element;
  id cursor = (__bridge id)element;
  for (int depth = 0; cursor && depth < 32 && !ACExpired(r); depth++) {
    if (CFEqual((__bridge CFTypeRef)cursor, root))
      return YES;
    cursor = ACCopy((__bridge AXUIElementRef)cursor, kAXParentAttribute, r);
  }
  return NO;
}
NSDictionary *ACObserve(ACSession *s, ACRequest *r, NSDictionary *input,
                        NSString **error) {
  ACWindow *w = s.windows[input[@"Window"][@"Handle"]];
  NSDictionary *d = input[@"Display"];
  *error = ACGuard(s, w, d, r, NO);
  if (*error)
    return nil;
  CGRect bounds;
  *error = ACReadWindow(w, &bounds);
  if (*error)
    return nil;
  if (!ACContains(ACRect(d[@"Bounds"]), bounds) ||
      w.displayID != [d[@"ID"] unsignedIntValue]) {
    *error = @"stale_window";
    return nil;
  }
  w.lastBounds = bounds;
  w.revision++;
  w.snapshotValid = YES;
  [w.elements removeAllObjects];
  NSMutableArray *nodes = [NSMutableArray new],
                 *pending = [NSMutableArray arrayWithObject:@{
                   @"Node" : w.element,
                   @"Depth" : @0
                 }];
  BOOL truncated = NO;
  while (pending.count && nodes.count < 128 && !ACExpired(r)) {
    NSDictionary *next = pending.lastObject;
    [pending removeLastObject];
    id object = next[@"Node"];
    NSUInteger depth = [next[@"Depth"] unsignedIntegerValue];
    if (CFGetTypeID((__bridge CFTypeRef)object) != AXUIElementGetTypeID())
      continue;
    AXUIElementRef element = (__bridge AXUIElementRef)object;
    CGRect rect = ACElementBounds(element, r);
    NSString *role = boundedString(ACCopy(element, kAXRoleAttribute, r), 128);
    NSArray *actions = nil;
    CFArrayRef actionNames = NULL;
    if (AXUIElementCopyActionNames(element, &actionNames) == kAXErrorSuccess)
      actions = CFBridgingRelease(actionNames);
    Boolean settable = false;
    AXUIElementIsAttributeSettable(element, kAXValueAttribute, &settable);
    if (ACContains(bounds, rect)) {
      NSString *handle = NSUUID.UUID.UUIDString;
      w.elements[handle] = object;
      NSString *value =
          [role isEqual:@"AXSecureTextField"]
              ? @""
              : boundedString(ACCopy(element, kAXValueAttribute, r), 4096);
      [nodes addObject:@{
        @"Handle" : handle,
        @"Role" : role,
        @"Title" : boundedString(ACCopy(element, kAXTitleAttribute, r), 512),
        @"Value" : value,
        @"Bounds" : ACBounds(rect),
        @"Press" : @([actions containsObject:(id)kAXPressAction]),
        @"SetValue" : [NSNumber numberWithBool:settable]
      }];
    }
    CFArrayRef childrenRef = NULL;
    NSArray *children = nil;
    if (!ACExpired(r) &&
        AXUIElementCopyAttributeValues(element, kAXChildrenAttribute, 0, 256,
                                       &childrenRef) == kAXErrorSuccess)
      children = CFBridgingRelease(childrenRef);
    if (children.count == 256)
      truncated = YES;
    if ([children isKindOfClass:NSArray.class] && children.count) {
      if (depth >= 12) {
        truncated = YES;
        continue;
      }
      for (id child in children) {
        if (pending.count >= 256) {
          truncated = YES;
          break;
        }
        [pending addObject:@{@"Node" : child, @"Depth" : @(depth + 1)}];
      }
    }
  }
  if (ACExpired(r)) {
    [w.elements removeAllObjects];
    *error = @"action_uncertain";
    return nil;
  }
  truncated |= pending.count > 0;
  CGFloat scale = CGDisplayPixelsWide(w.displayID) /
                  CGDisplayBounds(w.displayID).size.width;
  w.frameWidth = (uint32_t)ceil(bounds.size.width * scale);
  w.frameHeight = (uint32_t)ceil(bounds.size.height * scale);
  NSMutableDictionary *result = [@{
    @"Window" : ACWindowValue(w),
    @"Elements" : nodes,
    @"Width" : @(w.frameWidth),
    @"Height" : @(w.frameHeight),
    @"Truncated" : @(truncated)
  } mutableCopy];
  if ([input[@"PNG"] boolValue]) {
    NSData *png = ACScreenshot(s, r, w, d, error);
    if (!png)
      return nil;
    result[@"PNG"] = [png base64EncodedStringWithOptions:0];
  }
  return result;
}
static id selectedElement(ACWindow *w, NSDictionary *action, ACRequest *r) {
  NSString *kind = action[@"kind"];
  NSString *handle = [kind isEqual:@"type"]
                         ? action[@"type"][@"element_handle"]
                         : action[@"click"][@"element_handle"];
  if (handle)
    return w.elements[handle];
  NSDictionary *position = action[@"click"][@"position"];
  if (!position || !w.frameWidth || !w.frameHeight)
    return nil;
  double x = [position[@"x"] doubleValue], y = [position[@"y"] doubleValue];
  if (x < 0 || y < 0 || x >= w.frameWidth || y >= w.frameHeight)
    return nil;
  CGPoint point = CGPointMake(
      w.lastBounds.origin.x + x * w.lastBounds.size.width / w.frameWidth,
      w.lastBounds.origin.y + y * w.lastBounds.size.height / w.frameHeight);
  id chosen = nil;
  double area = DBL_MAX;
  for (id candidate in w.elements.allValues) {
    CGRect bounds = ACElementBounds((__bridge AXUIElementRef)candidate, r);
    double a = bounds.size.width * bounds.size.height;
    if (CGRectContainsPoint(bounds, point) && a < area) {
      chosen = candidate;
      area = a;
    }
  }
  return chosen;
}
NSDictionary *ACAction(ACSession *s, ACRequest *r, NSDictionary *input,
                       NSString **error) {
  ACWindow *w = s.windows[input[@"Window"][@"Handle"]];
  NSDictionary *d = input[@"Display"], *action = input[@"Action"];
  *error = ACGuard(s, w, d, r, YES);
  if (*error)
    return nil;
  CGRect bounds;
  *error = ACReadWindow(w, &bounds);
  if (*error)
    return nil;
  if (!ACContains(ACRect(d[@"Bounds"]), bounds) ||
      !CGRectEqualToRect(bounds, w.lastBounds) ||
      w.displayID != [d[@"ID"] unsignedIntValue] ||
      w.revision !=
          [input[@"Window"][@"SnapshotRevision"] unsignedLongLongValue] ||
      !w.snapshotValid) {
    *error = @"stale_window";
    return nil;
  }
  NSString *kind = action[@"kind"], *outcome = @"dispatched";
  id chosen = selectedElement(w, action, r);
  if (([kind isEqual:@"type"] ||
       ([kind isEqual:@"click"] && action[@"click"][@"element_handle"])) &&
      (!chosen || !withinWindow((__bridge AXUIElementRef)chosen, w, r))) {
    *error = @"stale_window";
    return nil;
  }
  BOOL semantic = NO;
  if (chosen && withinWindow((__bridge AXUIElementRef)chosen, w, r)) {
    if (!ACContains(w.lastBounds,
                    ACElementBounds((__bridge AXUIElementRef)chosen, r))) {
      *error = @"stale_window";
      return nil;
    }
    AXUIElementRef element = (__bridge AXUIElementRef)chosen;
    AXUIElementSetMessagingTimeout(
        element, (float)MAX(.001, MIN(.1, r.deadline - ACNow())));
    if ([kind isEqual:@"click"]) {
      CFArrayRef names = NULL;
      if (AXUIElementCopyActionNames(element, &names) == kAXErrorSuccess) {
        NSArray *actions = CFBridgingRelease(names);
        semantic = [actions containsObject:(id)kAXPressAction];
      }
      if (semantic) {
        *error = ACGuard(s, w, d, r, YES);
        if (*error)
          return nil;
        if (AXUIElementPerformAction(element, kAXPressAction) !=
            kAXErrorSuccess) {
          ACMarkUncertain(s, w, @"ax_action");
          *error = @"action_uncertain";
        }
      }
    } else if ([kind isEqual:@"type"]) {
      Boolean settable = false;
      if (AXUIElementIsAttributeSettable(element, kAXValueAttribute,
                                         &settable) == kAXErrorSuccess &&
          settable) {
        semantic = YES;
        NSString *text = action[@"type"][@"text"];
        *error = ACGuard(s, w, d, r, YES);
        if (*error)
          return nil;
        if (AXUIElementSetAttributeValue(element, kAXValueAttribute,
                                         (__bridge CFStringRef)text) !=
            kAXErrorSuccess) {
          ACMarkUncertain(s, w, @"ax_action");
          *error = @"action_uncertain";
        } else if ([ACCopy(element, kAXValueAttribute, r) isEqual:text])
          outcome = @"verified";
      }
    }
  }
  if (!semantic) {
    if (![input[@"CertifiedPID"] boolValue]) {
      *error = @"needs_intervention";
      return nil;
    }
    if ([kind isEqual:@"type"] || [kind isEqual:@"key"]) {
      AXUIElementRef app =
          AXUIElementCreateApplication([w.process[@"PID"] intValue]);
      id focused = ACCopy(app, kAXFocusedUIElementAttribute, r);
      CFRelease(app);
      if (!focused || !withinWindow((__bridge AXUIElementRef)focused, w, r) ||
          ([kind isEqual:@"type"] &&
           !CFEqual((__bridge CFTypeRef)focused, (__bridge CFTypeRef)chosen))) {
        *error = @"needs_intervention";
        return nil;
      }
    }
    *error = ACPIDAction(s, r, w, d, action);
  }
  [w.elements removeAllObjects];
  w.snapshotValid = NO;
  if (ACExpired(r) ||
      NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier ==
          [w.process[@"PID"] intValue])
    *error = @"action_uncertain";
  if (*error)
    return nil;
  return @{@"Outcome" : outcome};
}
