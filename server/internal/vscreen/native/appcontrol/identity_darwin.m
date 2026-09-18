// go:build darwin && cgo

#import "native_internal.h"
#import <Security/Security.h>
#import <Carbon/Carbon.h>

CGRect ACRect(NSDictionary *d) {
  return CGRectMake([d[@"X"] doubleValue], [d[@"Y"] doubleValue],
                    [d[@"Width"] doubleValue], [d[@"Height"] doubleValue]);
}
NSDictionary *ACBounds(CGRect r) {
  return @{
    @"X" : @(r.origin.x),
    @"Y" : @(r.origin.y),
    @"Width" : @(r.size.width),
    @"Height" : @(r.size.height)
  };
}
BOOL ACContains(CGRect d, CGRect w) {
  return !CGRectIsEmpty(d) && !CGRectIsEmpty(w) && CGRectContainsRect(d, w);
}
static NSDictionary *ACExecutableIdentity(pid_t pid, NSString *bundle) {
  char executable[PROC_PIDPATHINFO_MAXSIZE] = {0};
  if (proc_pidpath(pid, executable, sizeof(executable)) <= 0)
    return @{};
  SecCodeRef code = NULL;
  NSDictionary *attributes = @{(__bridge id)kSecGuestAttributePid : @(pid)};
  if (SecCodeCopyGuestWithAttributes(NULL, (__bridge CFDictionaryRef)attributes,
                                    kSecCSDefaultFlags, &code) != errSecSuccess)
    return @{};
  CFDictionaryRef information = NULL;
  OSStatus status = SecCodeCheckValidity(code, kSecCSDefaultFlags, NULL);
  if (status == errSecSuccess)
    status = SecCodeCopySigningInformation((SecStaticCodeRef)code,
                                           kSecCSDefaultFlags, &information);
  CFRelease(code);
  if (status != errSecSuccess || !information) {
    if (information)
      CFRelease(information);
    return @{};
  }
  NSDictionary *signedInfo = CFBridgingRelease(information);
  id identifier = signedInfo[(__bridge id)kSecCodeInfoIdentifier];
  id hash = signedInfo[(__bridge id)kSecCodeInfoUnique];
  id plist = signedInfo[(__bridge id)kSecCodeInfoPList];
  id signedExecutable = signedInfo[(__bridge id)kSecCodeInfoMainExecutable];
  NSString *path = [[NSString stringWithUTF8String:executable] stringByResolvingSymlinksInPath];
  if (![identifier isKindOfClass:NSString.class] || ![identifier length] ||
      ![hash isKindOfClass:NSData.class] || ([hash length] != 20 && [hash length] != 32) ||
      ![plist isKindOfClass:NSDictionary.class] ||
      ![signedExecutable isKindOfClass:NSURL.class] ||
      ![[signedExecutable path].stringByResolvingSymlinksInPath isEqual:path] ||
      ![plist[@"CFBundleIdentifier"] isEqual:bundle])
    return @{};
  id version = plist[@"CFBundleShortVersionString"], build = plist[@"CFBundleVersion"];
  if (![version isKindOfClass:NSString.class] || ![version length] ||
      ![build isKindOfClass:NSString.class] || ![build length])
    return @{};
  NSMutableString *codeHash = [NSMutableString new];
  const unsigned char *bytes = [hash bytes];
  for (NSUInteger i = 0; i < [hash length]; i++)
    [codeHash appendFormat:@"%02x", bytes[i]];
  return @{ @"ExecutablePath": path, @"SigningID": identifier, @"CodeHash": codeHash,
            @"AppVersion": version, @"AppBuild": build };
}

NSDictionary *ACProcess(pid_t pid) {
  struct proc_bsdinfo info = {0};
  if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, sizeof(info)) !=
          sizeof(info) ||
      info.pbi_uid != getuid())
    return nil;
  NSRunningApplication *app =
      [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
  if (!app || !app.bundleIdentifier)
    return nil;
  char build[128] = {0};
  size_t size = sizeof(build);
  if (sysctlbyname("kern.osversion", build, &size, NULL, 0) != 0)
    return nil;
  NSMutableDictionary *identity = [@{
    @"PID" : @(pid),
    @"UID" : @(info.pbi_uid),
    @"Start" : [NSString stringWithFormat:@"%llu:%llu", info.pbi_start_tvsec,
                                          info.pbi_start_tvusec],
    @"BundleID" : app.bundleIdentifier,
    @"OSBuild" : @(build)
  } mutableCopy];
  struct proc_bsdinfo after = {0};
  if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &after, sizeof(after)) != sizeof(after) ||
      after.pbi_uid != info.pbi_uid || after.pbi_start_tvsec != info.pbi_start_tvsec ||
      after.pbi_start_tvusec != info.pbi_start_tvusec)
    return nil;
  return identity;
}
NSDictionary *ACPIDProcess(pid_t pid) {
  NSDictionary *before = ACProcess(pid);
  if (!before)
    return nil;
  NSMutableDictionary *identity = [before mutableCopy];
  [identity addEntriesFromDictionary:ACExecutableIdentity(pid, before[@"BundleID"])];
  if (![ACProcess(pid) isEqual:before])
    return nil;
  return identity;
}
BOOL ACProcessEnded(NSDictionary *expected) {
  pid_t pid = [expected[@"PID"] intValue];
  struct proc_bsdinfo info = {0};
  if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, sizeof(info)) ==
      sizeof(info)) {
    NSString *start =
        [NSString stringWithFormat:@"%llu:%llu", info.pbi_start_tvsec,
                                   info.pbi_start_tvusec];
    return ![start isEqual:expected[@"Start"]];
  }
  return kill(pid, 0) == -1 && errno == ESRCH;
}
id ACCopy(AXUIElementRef element, CFStringRef attribute, ACRequest *r) {
  if (ACExpired(r))
    return nil;
  AXUIElementSetMessagingTimeout(
      element, (float)MAX(0.001, MIN(.1, r.deadline - ACNow())));
  CFTypeRef value = NULL;
  if (AXUIElementCopyAttributeValue(element, attribute, &value) !=
      kAXErrorSuccess)
    return nil;
  return CFBridgingRelease(value);
}
CGRect ACElementBounds(AXUIElementRef element, ACRequest *r) {
  id p = ACCopy(element, kAXPositionAttribute, r),
     z = ACCopy(element, kAXSizeAttribute, r);
  CGPoint point;
  CGSize size;
  if (!p || !z || CFGetTypeID((__bridge CFTypeRef)p) != AXValueGetTypeID() ||
      CFGetTypeID((__bridge CFTypeRef)z) != AXValueGetTypeID())
    return CGRectNull;
  if (!AXValueGetValue((__bridge AXValueRef)p, kAXValueCGPointType, &point) ||
      !AXValueGetValue((__bridge AXValueRef)z, kAXValueCGSizeType, &size))
    return CGRectNull;
  return (CGRect){point, size};
}
NSString *ACReadWindow(ACWindow *w, CGRect *bounds) {
  if (![ACProcess([w.process[@"PID"] intValue]) isEqual:w.process])
    return @"stale_window";
  NSArray *windows = CFBridgingRelease(CGWindowListCopyWindowInfo(
      kCGWindowListOptionIncludingWindow, w.windowID));
  for (NSDictionary *entry in windows) {
    if ([entry[(id)kCGWindowNumber] unsignedIntValue] != w.windowID)
      continue;
    if ([entry[(id)kCGWindowOwnerPID] intValue] != [w.process[@"PID"] intValue])
      return @"stale_window";
    if (!CGRectMakeWithDictionaryRepresentation(
            (__bridge CFDictionaryRef)entry[(id)kCGWindowBounds], bounds))
      return @"stale_window";
    return nil;
  }
  return @"stale_window";
}
NSString *ACGuard(ACSession *s, ACWindow *w, NSDictionary *d, ACRequest *r,
                  BOOL background) {
  if (ACExpired(r))
    return @"action_uncertain";
  if (!w || !AXIsProcessTrusted())
    return w ? @"accessibility_denied" : @"stale_window";
  CGRect bounds;
  NSString *error = ACReadWindow(w, &bounds);
  if (error)
    return error;
  CGDirectDisplayID display = [d[@"ID"] unsignedIntValue];
  CGRect expected = ACRect(d[@"Bounds"]);
  if (!CGDisplayIsOnline(display) ||
      !CGRectEqualToRect(CGDisplayBounds(display), expected))
    return @"source_gone";
  if ([d[@"Virtual"] boolValue] && (CGDisplayVendorNumber(display) != 0x4D55 ||
                                    CGDisplayModelNumber(display) != 1))
    return @"source_gone";
  if (background) {
    [s.lock lock];
    BOOL blocked =
        s.sessionBlocked || w.sessionGeneration != s.generation ||
        [s.blockedPIDs containsObject:w.process[@"PID"]];
    [s.lock unlock];
    if (blocked ||
        NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier ==
            [w.process[@"PID"] intValue])
      return @"needs_intervention";
    CFDictionaryRef session = CGSessionCopyCurrentDictionary();
    BOOL console =
        session &&
        [((__bridge NSDictionary *)session)[(id)kCGSessionOnConsoleKey]
            boolValue];
    BOOL locked =
        session &&
        [((__bridge NSDictionary *)session)[@"CGSSessionScreenIsLocked"]
            boolValue];
    if (session)
      CFRelease(session);
    if (!console || locked)
      return @"needs_intervention";
    AXUIElementRef app =
        AXUIElementCreateApplication([w.process[@"PID"] intValue]);
    NSArray *list = ACCopy(app, kAXWindowsAttribute, r);
    CFRelease(app);
    if (![list isKindOfClass:NSArray.class] || list.count != 1)
      return @"needs_intervention";
  }
  return ACExpired(r) ? @"action_uncertain" : nil;
}
NSDictionary *ACWindowValue(ACWindow *w) {
  return @{
    @"Handle" : w.handle,
    @"Process" : w.process,
    @"WindowID" : @(w.windowID),
    @"DisplayID" : @(w.displayID),
    @"Bounds" : ACBounds(w.lastBounds),
    @"OriginalBounds" : ACBounds(w.original),
    @"SnapshotRevision" : @(w.snapshotValid ? w.revision : 0)
  };
}

NSString *ACInputSourceID(void) {
  TISInputSourceRef source = TISCopyCurrentKeyboardInputSource();
  id sourceID = source ? (__bridge id)TISGetInputSourceProperty(source, kTISPropertyInputSourceID) : nil;
  NSString *result = [sourceID isKindOfClass:NSString.class] ? [sourceID copy] : @"";
  if (source)
    CFRelease(source);
  return result;
}
char *ac_input_source(void) {
  @autoreleasepool {
    return strdup(ACInputSourceID().UTF8String);
  }
}
