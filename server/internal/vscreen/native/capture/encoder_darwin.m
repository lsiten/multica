//go:build darwin && cgo

#import "encoder_internal.h"
#import <VideoToolbox/VideoToolbox.h>
#include "live_resources.h"

static const NSUInteger maxSampleBytes = 8 * 1024 * 1024;
@interface VSSample : NSObject
@property NSData *bytes;
@property int64_t pts;
@property int64_t duration;
@property BOOL keyframe;
@end
@implementation VSSample
@end

@interface VSEncoder () {
  VTCompressionSessionRef _session;
  BOOL _telemetryCounted;
  NSCondition *_condition;
  dispatch_queue_t _queue;
  CVPixelBufferRef _pending;
  CMTime _pendingPTS, _pendingDuration;
  NSMutableArray<VSSample *> *_samples;
  BOOL _scheduled, _closing, _closed, _forceKeyframe, _awaitingIDR;
  int _failure;
  uint32_t _inFlight, _peakInFlight, _peakQueued;
  uint64_t _droppedInput, _droppedOutput;
  BOOL _hardware, _hardwareKnown;
  char _profileLevel[7];
  VSCaptureConfig _config;
}
- (void)output:(CMSampleBufferRef)sample status:(OSStatus)status;
- (void)drain;
@end

static void encoded(void *context, void *source, OSStatus status,
                    VTEncodeInfoFlags flags, CMSampleBufferRef sample) {
  @autoreleasepool {
    BOOL counted=vs_live_enter(VS_LIVE_CALLBACK);
    @try {[(__bridge VSEncoder *)context output:sample status:status];}
    @finally {if(counted)vs_live_exit(VS_LIVE_CALLBACK);}
  }
}
@implementation VSEncoder
- (instancetype)initWithConfig:(VSCaptureConfig)config status:(int *)status {
  if (!(self = [super init])) {
    *status = 4;
    return nil;
  }
  _condition = [NSCondition new];
  _queue =
      dispatch_queue_create("ai.multica.vscreen.encode", DISPATCH_QUEUE_SERIAL);
  _samples = [NSMutableArray new];
  _config = config;
  _config.excluded = NULL;
  _config.excluded_count = 0;
  _forceKeyframe = YES;
  _awaitingIDR = YES;
  NSDictionary *spec = @{
    (__bridge NSString *)
    kVTVideoEncoderSpecification_EnableHardwareAcceleratedVideoEncoder : @YES
  };
  OSStatus result = VTCompressionSessionCreate(
      NULL, config.width, config.height, kCMVideoCodecType_H264,
      (__bridge CFDictionaryRef)spec, NULL, NULL, encoded,
      (__bridge void *)self, &_session);
  if (result != noErr) {
    *status = 4;
    return nil;
  }
  _telemetryCounted=vs_live_enter(VS_LIVE_ENCODER);
  NSDictionary *properties = @{
    (__bridge NSString *)kVTCompressionPropertyKey_RealTime : @YES,
    (__bridge NSString *)kVTCompressionPropertyKey_AllowFrameReordering : @NO,
    (__bridge NSString *)kVTCompressionPropertyKey_ProfileLevel :
        (__bridge NSString *)kVTProfileLevel_H264_ConstrainedBaseline_AutoLevel,
    (__bridge NSString *)
    kVTCompressionPropertyKey_ExpectedFrameRate : @(config.fps),
    (__bridge NSString *)
    kVTCompressionPropertyKey_MaxKeyFrameInterval : @(config.fps * 2),
    (__bridge NSString *)
    kVTCompressionPropertyKey_AverageBitRate : @(config.bitrate)
  };
  result =
      VTSessionSetProperties(_session, (__bridge CFDictionaryRef)properties);
  if (result == noErr)
    result = VTCompressionSessionPrepareToEncodeFrames(_session);
  if (result != noErr) {
    VTCompressionSessionInvalidate(_session);
    CFRelease(_session);
    _session = NULL;
    if(_telemetryCounted){vs_live_exit(VS_LIVE_ENCODER);_telemetryCounted=NO;}
    *status = 4;
    return nil;
  }
  CFTypeRef hardware = NULL;
  if (VTSessionCopyProperty(
          _session,
          kVTCompressionPropertyKey_UsingHardwareAcceleratedVideoEncoder, NULL,
          &hardware) == noErr &&
      hardware) {
    _hardware = CFEqual(hardware, kCFBooleanTrue);
    _hardwareKnown = YES;
    CFRelease(hardware);
  }
  *status = 0;
  return self;
}
- (void)dealloc {
  if (_pending)
    CVPixelBufferRelease(_pending);
  if (_session) {
    VTCompressionSessionInvalidate(_session);
    CFRelease(_session);
    if(_telemetryCounted){vs_live_exit(VS_LIVE_ENCODER);_telemetryCounted=NO;}
  }
}
- (void)fail:(int)status {
  [_condition lock];
  _failure = status;
  [_condition broadcast];
  [_condition unlock];
}
- (void)enqueueBuffer:(CVPixelBufferRef)buffer
                  pts:(CMTime)pts
             duration:(CMTime)duration {
  [_condition lock];
  if (_closing || _failure) {
    [_condition unlock];
    return;
  }
  if (_pending) {
    CVPixelBufferRelease(_pending);
    _droppedInput++;
  }
  _pending = CVPixelBufferRetain(buffer);
  _pendingPTS = pts;
  _pendingDuration = duration;
  if (!_scheduled) {
    _scheduled = YES;
    dispatch_async(_queue, ^{
      @autoreleasepool {
        [self drain];
      }
    });
  }
  [_condition unlock];
}
- (void)drain {
  for (;;) {
    [_condition lock];
    if (_closing || !_pending || _inFlight >= 2) {
      _scheduled = NO;
      [_condition unlock];
      return;
    }
    CVPixelBufferRef buffer = _pending;
    _pending = NULL;
    CMTime pts = _pendingPTS, duration = _pendingDuration;
    BOOL force = _forceKeyframe;
    _forceKeyframe = NO;
    _inFlight++;
    _peakInFlight = MAX(_peakInFlight, _inFlight);
    [_condition unlock];
    NSDictionary *options = force ? @{
      (__bridge NSString *)kVTEncodeFrameOptionKey_ForceKeyFrame : @YES
    }
                                  : nil;
    OSStatus status = VTCompressionSessionEncodeFrame(
        _session, buffer, pts, duration, (__bridge CFDictionaryRef)options,
        NULL, NULL);
    CVPixelBufferRelease(buffer);
    if (status != noErr) {
      [_condition lock];
      if (_inFlight)
        _inFlight--;
      [_condition unlock];
      [self fail:4];
      return;
    }
  }
}
- (void)output:(CMSampleBufferRef)sample status:(OSStatus)status {
  [_condition lock];
  if (_inFlight)
    _inFlight--;
  if (!_closing && _pending && !_scheduled) {
    _scheduled = YES;
    dispatch_async(_queue, ^{
      @autoreleasepool {
        [self drain];
      }
    });
  }
  [_condition unlock];
  if (status != noErr || !sample || !CMSampleBufferDataIsReady(sample)) {
    if (status != noErr)
      [self fail:4];
    return;
  }
  CFArrayRef attachments =
      CMSampleBufferGetSampleAttachmentsArray(sample, false);
  BOOL keyframe = YES;
  if (attachments && CFArrayGetCount(attachments) > 0) {
    CFDictionaryRef entry = CFArrayGetValueAtIndex(attachments, 0);
    keyframe = !CFDictionaryContainsKey(entry, kCMSampleAttachmentKey_NotSync);
  }
  [_condition lock];
  BOOL discard = _closing || (_awaitingIDR && !keyframe);
  [_condition unlock];
  if (discard)
    return;
  CMFormatDescriptionRef format = CMSampleBufferGetFormatDescription(sample);
  NSMutableData *annex = [NSMutableData data];
  const uint8_t startCode[4] = {0, 0, 0, 1};
  int nalLengthSize = 0;
  size_t count = 0;
  if (CMVideoFormatDescriptionGetH264ParameterSetAtIndex(
          format, 0, NULL, NULL, &count, &nalLengthSize) != noErr ||
      nalLengthSize < 1 || nalLengthSize > 4) {
    [self fail:4];
    return;
  }
  if (keyframe) {
    for (size_t index = 0; index < count; index++) {
      const uint8_t *parameter = NULL;
      size_t length = 0;
      if (CMVideoFormatDescriptionGetH264ParameterSetAtIndex(
              format, index, &parameter, &length, NULL, NULL) != noErr ||
          annex.length > maxSampleBytes - 4 ||
          length > maxSampleBytes - annex.length - 4) {
        [self fail:4];
        return;
      }
      if (index == 0) {
        if (length < 4 ||
            (_config.max_level && parameter[3] > _config.max_level)) {
          [self fail:4];
          return;
        }
        [_condition lock];
        snprintf(_profileLevel, sizeof(_profileLevel), "%02x%02x%02x",
                 parameter[1], parameter[2], parameter[3]);
        [_condition unlock];
      }
      [annex appendBytes:startCode length:4];
      [annex appendBytes:parameter length:length];
    }
  }
  CMBlockBufferRef block = CMSampleBufferGetDataBuffer(sample);
  size_t total = block ? CMBlockBufferGetDataLength(block) : 0;
  if (!total || total > maxSampleBytes) {
    [self fail:4];
    return;
  }
  NSMutableData *avcc = [NSMutableData dataWithLength:total];
  if (CMBlockBufferCopyDataBytes(block, 0, total, avcc.mutableBytes) !=
      kCMBlockBufferNoErr) {
    [self fail:4];
    return;
  }
  const uint8_t *bytes = avcc.bytes;
  size_t offset = 0;
  while (offset < total) {
    if (total - offset < (size_t)nalLengthSize) {
      [self fail:4];
      return;
    }
    uint32_t length = 0;
    for (int i = 0; i < nalLengthSize; i++)
      length = (length << 8) | bytes[offset++];
    if (!length || length > total - offset ||
        annex.length + 4 + length > maxSampleBytes) {
      [self fail:4];
      return;
    }
    [annex appendBytes:startCode length:4];
    [annex appendBytes:bytes + offset length:length];
    offset += length;
  }
  CMTime pts = CMSampleBufferGetPresentationTimeStamp(sample);
  CMTime duration = CMSampleBufferGetDuration(sample);
  if (!CMTIME_IS_NUMERIC(pts)) {
    [self fail:4];
    return;
  }
  VSSample *output = [VSSample new];
  output.bytes = annex;
  output.pts =
      CMTimeConvertScale(pts, 1000000000, kCMTimeRoundingMethod_Default).value;
  output.duration = CMTIME_IS_NUMERIC(duration)
                        ? CMTimeConvertScale(duration, 1000000000,
                                             kCMTimeRoundingMethod_Default)
                              .value
                        : 1000000000 / _config.fps;
  output.keyframe = keyframe;
  [_condition lock];
  if (!_closing) {
    if (_samples.count >= 3) {
      _droppedOutput += _samples.count;
      [_samples removeAllObjects];
      _awaitingIDR = YES;
      _forceKeyframe = YES;
    }
    if (keyframe) {
      _awaitingIDR = NO;
    }
    if (!_awaitingIDR) {
      [_samples addObject:output];
      _peakQueued = MAX(_peakQueued, (uint32_t)_samples.count);
      [_condition signal];
    }
  }
  [_condition unlock];
}
- (int)next:(VSEncodedSample *)sample timeout:(uint32_t)milliseconds {
  NSDate *deadline =
      [NSDate dateWithTimeIntervalSinceNow:milliseconds / 1000.0];
  [_condition lock];
  while (!_samples.count && !_closing && !_failure) {
    if (![_condition waitUntilDate:deadline]) {
      [_condition unlock];
      return 9;
    }
  }
  if (_closing || _failure) {
    int status = _failure ?: 6;
    [_condition unlock];
    return status;
  }
  VSSample *output = _samples.firstObject;
  [_samples removeObjectAtIndex:0];
  sample->length = (uint32_t)output.bytes.length;
  sample->bytes = malloc(sample->length);
  if (!sample->bytes) {
    [_condition unlock];
    return 4;
  }
  memcpy(sample->bytes, output.bytes.bytes, sample->length);
  sample->pts_ns = output.pts;
  sample->duration_ns = output.duration;
  sample->keyframe = output.keyframe;
  [_condition unlock];
  return 0;
}
- (int)forceKeyframe {
  [_condition lock];
  if (_closing) {
    [_condition unlock];
    return 6;
  }
  _forceKeyframe = YES;
  [_condition unlock];
  return 0;
}
- (int)close:(uint32_t)milliseconds {
  NSDate *deadline =
      [NSDate dateWithTimeIntervalSinceNow:milliseconds / 1000.0];
  [_condition lock];
  if (!_closing) {
    _closing = YES;
    if (_pending) {
      CVPixelBufferRelease(_pending);
      _pending = NULL;
    }
    [_samples removeAllObjects];
    [_condition broadcast];
    dispatch_async(_queue, ^{
      VTCompressionSessionCompleteFrames(self->_session, kCMTimeInvalid);
      VTCompressionSessionInvalidate(self->_session);
      CFRelease(self->_session);
      self->_session = NULL;
      if(self->_telemetryCounted){vs_live_exit(VS_LIVE_ENCODER);self->_telemetryCounted=NO;}
      [self->_condition lock];
      self->_closed = YES;
      [self->_condition broadcast];
      [self->_condition unlock];
    });
  }
  while (!_closed) {
    if (![_condition waitUntilDate:deadline]) {
      [_condition unlock];
      return 5;
    }
  }
  [_condition unlock];
  return 0;
}
- (void)stats:(VSStreamStats *)stats {
  [_condition lock];
  *stats = (VSStreamStats){
      _pending ? 1 : 0, _inFlight,     (uint32_t)_samples.count, _peakInFlight,
      _peakQueued,      _droppedInput, _droppedOutput,           _hardware,
      _hardwareKnown,   _closed};
  memcpy(stats->profile_level, _profileLevel, sizeof(_profileLevel));
  [_condition unlock];
}
- (int)fixtureFrame:(uint32_t)index {
  CVPixelBufferRef buffer = NULL;
  NSDictionary *attributes =
      @{(__bridge NSString *)kCVPixelBufferIOSurfacePropertiesKey : @{}};
  CVReturn status = CVPixelBufferCreate(
      NULL, _config.width, _config.height, kCVPixelFormatType_32BGRA,
      (__bridge CFDictionaryRef)attributes, &buffer);
  if (status != kCVReturnSuccess)
    return 4;
  CVPixelBufferLockBaseAddress(buffer, 0);
  uint8_t *base = CVPixelBufferGetBaseAddress(buffer);
  size_t stride = CVPixelBufferGetBytesPerRow(buffer);
  for (uint32_t y = 0; y < _config.height; y++)
    for (uint32_t x = 0; x < _config.width; x++) {
      uint8_t *pixel = base + y * stride + x * 4;
      pixel[0] = (uint8_t)(x + index * 3);
      pixel[1] = (uint8_t)y;
      pixel[2] = (uint8_t)(index * 29);
      pixel[3] = 255;
    }
  CVPixelBufferUnlockBaseAddress(buffer, 0);
  [self enqueueBuffer:buffer
                  pts:CMTimeMake(index, _config.fps)
             duration:CMTimeMake(1, _config.fps)];
  CVPixelBufferRelease(buffer);
  return 0;
}
@end
