// go:build darwin && cgo

#import "native_internal.h"

NSData *ACScreenshot(ACSession *s, ACRequest *r, ACWindow *w, NSDictionary *d,
                     NSString **error) {
  if (!CGPreflightScreenCaptureAccess()) {
    *error = @"screen_recording_denied";
    return nil;
  }
  if (@available(macOS 14.0, *)) {
    uint32_t width =
        w ? w.frameWidth
          : (uint32_t)CGDisplayPixelsWide([d[@"ID"] unsignedIntValue]);
    uint32_t height =
        w ? w.frameHeight
          : (uint32_t)CGDisplayPixelsHigh([d[@"ID"] unsignedIntValue]);
    if (!width || !height || width > 8192 || height > 8192) {
      *error = @"needs_intervention";
      return nil;
    }
    dispatch_semaphore_t completed = dispatch_semaphore_create(0);
    __block NSData *png = nil;
    dispatch_group_enter(s.pending);
    [SCShareableContent
        getShareableContentExcludingDesktopWindows:NO
                               onScreenWindowsOnly:NO
                                 completionHandler:^(
                                     SCShareableContent *content,
                                     NSError *failure) {
                                   SCContentFilter *filter = nil;
                                   if (w) {
                                     for (SCWindow *candidate in content
                                              .windows) {
                                       if (candidate.windowID == w.windowID &&
                                           candidate.owningApplication
                                                   .processID ==
                                               [w.process[@"PID"] intValue]) {
                                         filter = [[SCContentFilter alloc]
                                             initWithDesktopIndependentWindow:
                                                 candidate];
                                         break;
                                       }
                                     }
                                   } else {
                                     for (SCDisplay *candidate in content
                                              .displays) {
                                       if (candidate.displayID ==
                                           [d[@"ID"] unsignedIntValue]) {
                                         filter = [[SCContentFilter alloc]
                                              initWithDisplay:candidate
                                             excludingWindows:@[]];
                                         break;
                                       }
                                     }
                                   }
                                   if (failure || !filter || ACExpired(r)) {
                                     dispatch_group_leave(s.pending);
                                     dispatch_semaphore_signal(completed);
                                     return;
                                   }
                                   SCStreamConfiguration *config =
                                       [SCStreamConfiguration new];
                                   config.width = width;
                                   config.height = height;
                                   config.showsCursor = NO;
                                   config.capturesAudio = NO;
                                   config.ignoreShadowsSingleWindow = YES;
                                   config.scalesToFit = YES;
                                   config.preservesAspectRatio = YES;
                                   if (@available(macOS 15.0, *))
                                     config.captureMicrophone = NO;
                                   [SCScreenshotManager
                                       captureImageWithFilter:filter
                                                configuration:config
                                            completionHandler:^(
                                                CGImageRef image,
                                                NSError *captureError) {
                                              if (!captureError && image &&
                                                  !ACExpired(r)) {
                                                NSMutableData *encoded =
                                                    [NSMutableData new];
                                                CGImageDestinationRef destination =
                                                    CGImageDestinationCreateWithData(
                                                        (__bridge CFMutableDataRef)
                                                            encoded,
                                                        (__bridge CFStringRef)
                                                            UTTypePNG
                                                                .identifier,
                                                        1, NULL);
                                                if (destination) {
                                                  CGImageDestinationAddImage(
                                                      destination, image, NULL);
                                                  if (CGImageDestinationFinalize(
                                                          destination) &&
                                                      encoded.length <=
                                                          8 * 1024 * 1024)
                                                    png = encoded;
                                                  CFRelease(destination);
                                                }
                                              }
                                              dispatch_group_leave(s.pending);
                                              dispatch_semaphore_signal(
                                                  completed);
                                            }];
                                 }];
    if (dispatch_semaphore_wait(
            completed, dispatch_time(DISPATCH_TIME_NOW,
                                     (int64_t)(MAX(0, r.deadline - ACNow()) *
                                               NSEC_PER_SEC))) != 0 ||
        ACExpired(r)) {
      *error = @"action_uncertain";
      return nil;
    }
    if (!png) {
      *error = @"source_gone";
      return nil;
    }
    if (!w) {
      uint32_t id = [d[@"ID"] unsignedIntValue];
      if (!CGDisplayIsOnline(id) ||
          !CGRectEqualToRect(CGDisplayBounds(id), ACRect(d[@"Bounds"]))) {
        *error = @"source_gone";
        return nil;
      }
      return png;
    }
    CGRect bounds;
    *error = ACGuard(s, w, d, r, NO);
    if (*error)
      return nil;
    *error = ACReadWindow(w, &bounds);
    if (*error || !CGRectEqualToRect(bounds, w.lastBounds)) {
      *error = @"stale_window";
      return nil;
    }
    return png;
  }
  *error = @"native_unavailable";
  return nil;
}

NSDictionary *ACObserveDisplay(ACSession *s, ACRequest *r, NSDictionary *input,
                               NSString **error) {
  NSDictionary *d = input[@"Display"];
  uint32_t identifier = [d[@"ID"] unsignedIntValue];
  if (![d[@"Virtual"] boolValue] || !CGDisplayIsOnline(identifier) ||
      CGDisplayVendorNumber(identifier) != 0x4D55 ||
      CGDisplayModelNumber(identifier) != 1 ||
      !CGRectEqualToRect(CGDisplayBounds(identifier), ACRect(d[@"Bounds"]))) {
    *error = @"source_gone";
    return nil;
  }
  NSMutableDictionary *result = [@{
    @"Width" : @(CGDisplayPixelsWide(identifier)),
    @"Height" : @(CGDisplayPixelsHigh(identifier)),
    @"Elements" : @[]
  } mutableCopy];
  if ([input[@"PNG"] boolValue]) {
    NSData *png = ACScreenshot(s, r, nil, d, error);
    if (!png)
      return nil;
    result[@"PNG"] = [png base64EncodedStringWithOptions:0];
  }
  return result;
}
