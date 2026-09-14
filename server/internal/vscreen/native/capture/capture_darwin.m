//go:build darwin && cgo

#import "encoder_internal.h"
#import <CoreGraphics/CoreGraphics.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#include <stdatomic.h>

static atomic_uint liveCaptures = 0;

@interface VSCapture : NSObject <SCStreamOutput, SCStreamDelegate> {
  SCStream *_stream;
  VSEncoder *_encoder;
  dispatch_queue_t _deliveryQueue;
  NSCondition *_condition;
  BOOL _closing, _closed, _stopRequested, _counted;
  int _stopError;
}
- (instancetype)initWithConfig:(VSCaptureConfig)config
                       content:(SCShareableContent *)content
                       display:(SCDisplay *)display
                        status:(int *)status;
- (int)start;
- (int)next:(VSEncodedSample *)sample timeout:(uint32_t)milliseconds;
- (int)forceKeyframe;
- (void)stats:(VSStreamStats *)stats;
- (int)close:(uint32_t)milliseconds;
@end

@implementation VSCapture
- (instancetype)initWithConfig:(VSCaptureConfig)config
                       content:(SCShareableContent *)content
                       display:(SCDisplay *)display
                        status:(int *)status {
  if (!(self = [super init])) {
    *status = 7;
    return nil;
  }
  _counted = YES;
  if (atomic_fetch_add(&liveCaptures, 1) >= 16) {
    *status = 7;
    return nil;
  }
  _condition = [NSCondition new];
  _deliveryQueue = dispatch_queue_create("ai.multica.vscreen.capture",
                                         DISPATCH_QUEUE_SERIAL);
  _encoder = [[VSEncoder alloc] initWithConfig:config status:status];
  if (!_encoder)
    return nil;
  NSMutableArray<SCWindow *> *excluded = [NSMutableArray new];
  for (SCWindow *window in content.windows)
    for (uint32_t i = 0; i < config.excluded_count; i++)
      if (window.windowID == config.excluded[i]) {
        [excluded addObject:window];
        break;
      }
  SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:display
                                                    excludingWindows:excluded];
  SCStreamConfiguration *settings = [SCStreamConfiguration new];
  settings.width = config.width;
  settings.height = config.height;
  settings.minimumFrameInterval = CMTimeMake(1, config.fps);
  // Capture the full logical display, fitting it into a centered output
  // rectangle.
  CGRect source =
      CGRectMake(0, 0, display.frame.size.width, display.frame.size.height);
  if (source.size.width <= 0 || source.size.height <= 0) {
    [_encoder close:5000];
    *status = 3;
    return nil;
  }
  CGFloat factor =
      MIN(config.width / source.size.width, config.height / source.size.height);
  CGSize contentSize =
      CGSizeMake(source.size.width * factor, source.size.height * factor);
  settings.sourceRect = source;
  settings.destinationRect =
      CGRectMake((config.width - contentSize.width) / 2,
                 (config.height - contentSize.height) / 2, contentSize.width,
                 contentSize.height);
  settings.scalesToFit = YES;
  if (@available(macOS 14.0, *))
    settings.preservesAspectRatio = YES;
  settings.backgroundColor = CGColorGetConstantColor(kCGColorBlack);
  settings.queueDepth = 3;
  settings.pixelFormat = kCVPixelFormatType_32BGRA;
  settings.showsCursor = config.cursor != 0;
  if (@available(macOS 13.0, *))
    settings.capturesAudio = NO;
  if (@available(macOS 15.0, *))
    settings.captureMicrophone = NO;
  _stream = [[SCStream alloc] initWithFilter:filter
                               configuration:settings
                                    delegate:self];
  NSError *error = nil;
  if (![_stream addStreamOutput:self
                           type:SCStreamOutputTypeScreen
             sampleHandlerQueue:_deliveryQueue
                          error:&error]) {
    [_encoder close:5000];
    *status = 7;
    return nil;
  }
  *status = 0;
  return self;
}
- (void)dealloc {
  if (_counted)
    atomic_fetch_sub(&liveCaptures, 1);
}
- (int)start {
  dispatch_semaphore_t ready = dispatch_semaphore_create(0);
  __block int status = 0;
  [_stream startCaptureWithCompletionHandler:^(NSError *error) {
    status = error ? 7 : 0;
    dispatch_semaphore_signal(ready);
  }];
  if (dispatch_semaphore_wait(
          ready, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC)) != 0)
    return 5;
  return status;
}
- (void)stream:(SCStream *)stream
    didOutputSampleBuffer:(CMSampleBufferRef)sample
                   ofType:(SCStreamOutputType)type {
  if (type != SCStreamOutputTypeScreen || !CMSampleBufferIsValid(sample) ||
      !CMSampleBufferDataIsReady(sample))
    return;
  [_condition lock];
  BOOL closing = _closing;
  [_condition unlock];
  if (closing)
    return;
  CFArrayRef attachments =
      CMSampleBufferGetSampleAttachmentsArray(sample, false);
  if (!attachments || CFArrayGetCount(attachments) == 0)
    return;
  NSDictionary *metadata =
      (__bridge NSDictionary *)CFArrayGetValueAtIndex(attachments, 0);
  NSNumber *status = metadata[SCStreamFrameInfoStatus];
  if (!status || status.integerValue != SCFrameStatusComplete)
    return;
  CVPixelBufferRef buffer = CMSampleBufferGetImageBuffer(sample);
  if (!buffer)
    return;
  [_encoder enqueueBuffer:buffer
                      pts:CMSampleBufferGetPresentationTimeStamp(sample)
                 duration:CMSampleBufferGetDuration(sample)];
}
- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
  [_encoder fail:CGPreflightScreenCaptureAccess() ? 7 : 2];
}
- (int)next:(VSEncodedSample *)sample timeout:(uint32_t)milliseconds {
  return [_encoder next:sample timeout:milliseconds];
}
- (void)stats:(VSStreamStats *)stats {
  [_encoder stats:stats];
}
- (int)forceKeyframe {
  return [_encoder forceKeyframe];
}
- (int)close:(uint32_t)milliseconds {
  NSDate *deadline =
      [NSDate dateWithTimeIntervalSinceNow:milliseconds / 1000.0];
  [_condition lock];
  _closing = YES;
  BOOL requestStop = !_stopRequested;
  _stopRequested = YES;
  [_condition unlock];
  if (requestStop) {
    [_stream stopCaptureWithCompletionHandler:^(NSError *error) {
      if (error) {
        [self->_condition lock];
        self->_stopError = 7;
        [self->_condition broadcast];
        [self->_condition unlock];
        return;
      }
      NSError *removeError = nil;
      if (![self->_stream removeStreamOutput:self
                                        type:SCStreamOutputTypeScreen
                                       error:&removeError]) {
        [self->_condition lock];
        self->_stopError = 7;
        [self->_condition broadcast];
        [self->_condition unlock];
        return;
      }
      // The serial queue barrier runs after all delivered capture callbacks.
      dispatch_async(self->_deliveryQueue, ^{
        int status = [self->_encoder close:5000];
        [self->_condition lock];
        self->_stopError = status;
        self->_closed = status == 0;
        [self->_condition broadcast];
        [self->_condition unlock];
      });
    }];
  }
  [_condition lock];
  while (!_closed && !_stopError) {
    if (![_condition waitUntilDate:deadline]) {
      [_condition unlock];
      return 5;
    }
  }
  int status = _stopError;
  [_condition unlock];
  return status;
}
@end

int vs_capture_open(VSCaptureConfig config, uintptr_t *handle) {
  @autoreleasepool {
    if (!CGPreflightScreenCaptureAccess())
      return 2;
    if (@available(macOS 12.3, *)) {
      dispatch_semaphore_t ready = dispatch_semaphore_create(0);
      __block SCShareableContent *content = nil;
      __block BOOL failed = NO;
      [SCShareableContent
          getShareableContentExcludingDesktopWindows:YES
                                 onScreenWindowsOnly:NO
                                   completionHandler:^(
                                       SCShareableContent *result,
                                       NSError *error) {
                                     content = result;
                                     failed = error != nil;
                                     dispatch_semaphore_signal(ready);
                                   }];
      if (dispatch_semaphore_wait(
              ready, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC)) != 0)
        return 5;
      if (failed || !content)
        return 7;
      SCDisplay *display = nil;
      for (SCDisplay *candidate in content.displays)
        if (candidate.displayID == config.display_id) {
          display = candidate;
          break;
        }
      if (!display)
        return 3;
      int status = 0;
      VSCapture *capture = [[VSCapture alloc] initWithConfig:config
                                                     content:content
                                                     display:display
                                                      status:&status];
      if (!capture)
        return status;
      status = [capture start];
      if (status) {
        [capture close:5000];
        return status;
      }
      *handle = (uintptr_t)CFBridgingRetain(capture);
      return 0;
    }
    return 1;
  }
}
int vs_encoder_fixture(VSCaptureConfig config, uintptr_t *handle) {
  @autoreleasepool {
    int status = 0;
    VSEncoder *encoder = [[VSEncoder alloc] initWithConfig:config
                                                    status:&status];
    if (!encoder)
      return status;
    *handle = (uintptr_t)CFBridgingRetain(encoder);
    return 0;
  }
}
int vs_encoder_fixture_frame(uintptr_t handle, uint32_t index) {
  return [(__bridge VSEncoder *)(void *)handle fixtureFrame:index];
}
int vs_capture_next(uintptr_t handle, uint32_t timeout,
                    VSEncodedSample *sample) {
  @autoreleasepool {
    return [(__bridge VSCapture *)(void *)handle next:sample timeout:timeout];
  }
}
int vs_capture_force_keyframe(uintptr_t handle) {
  return [(__bridge VSCapture *)(void *)handle forceKeyframe];
}
int vs_capture_close(uintptr_t handle, uint32_t timeout) {
  int status = [(__bridge VSCapture *)(void *)handle close:timeout];
  if (!status) {
    id object = CFBridgingRelease((void *)handle);
    object = nil;
  }
  return status;
}
void vs_sample_free(VSEncodedSample *sample) {
  free(sample->bytes);
  memset(sample, 0, sizeof(*sample));
}

int vs_capture_stats(uintptr_t handle, VSStreamStats *stats) {
  [(__bridge VSCapture *)(void *)handle stats:stats];
  return 0;
}
int vs_capture_permission(void) {
  return CGPreflightScreenCaptureAccess() ? 0 : 2;
}
