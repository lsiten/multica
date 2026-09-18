#import "bridge.h"
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <Foundation/Foundation.h>

@interface VSEncoder : NSObject
- (instancetype)initWithConfig:(VSCaptureConfig)config status:(int *)status;
- (void)enqueueBuffer:(CVPixelBufferRef)buffer
                  pts:(CMTime)pts
             duration:(CMTime)duration;
- (int)next:(VSEncodedSample *)sample timeout:(uint32_t)milliseconds;
- (int)forceKeyframe;
- (int)close:(uint32_t)milliseconds;
- (int)fixtureFrame:(uint32_t)index;
- (void)fail:(int)status;
- (void)stats:(VSStreamStats *)stats;
@end
