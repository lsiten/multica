#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>

@interface ACWindow : NSObject
@property CGRect lastBounds;
@property uint32_t windowID;
@end
@implementation ACWindow
@end
#include "routing.inc"

// This probe inspects synthesized events without posting any input.
int main(void) {
  @autoreleasepool {
    ACWindow *window = [ACWindow new];
    window.windowID = 123;
    CGEventType types[] = {kCGEventLeftMouseDown, kCGEventLeftMouseUp,
                          kCGEventLeftMouseDragged, kCGEventScrollWheel};
    for (int placement = 0; placement < 2; placement++) {
      window.lastBounds = CGRectMake(placement ? -1400 : 3432, 106, 640, 508);
      for (unsigned i = 0; i < sizeof(types) / sizeof(types[0]); i++) {
        CGEventSourceRef source = CGEventSourceCreate(kCGEventSourceStatePrivate);
        CGEventRef original = types[i] == kCGEventScrollWheel
            ? CGEventCreateScrollWheelEvent(source, kCGScrollEventUnitPixel, 2, -120, 15)
            : CGEventCreateMouseEvent(source, types[i], CGPointZero, kCGMouseButtonLeft);
        CFRelease(source);
        CGEventSetLocation(original, CGPointMake(window.lastBounds.origin.x + 90, 304));
        double scrollY = CGEventGetDoubleValueField(original, kCGScrollWheelEventFixedPtDeltaAxis1);
        CGEventRef routed = routeWindowPointer(window, original);
        if (!routed) return 1;
        CGPoint point = CGEventGetLocation(routed);
        if (fabs(point.x - 90) > .01 || fabs(point.y - 198) > .01) {
          fprintf(stderr, "window-relative point lost: %.2f,%.2f\n", point.x, point.y);
          return 2;
        }
        if (CGEventGetType(routed) != types[i] ||
            (types[i] != kCGEventScrollWheel &&
             (CGEventGetIntegerValueField(routed, kCGMouseEventWindowUnderMousePointer) != 123 ||
              CGEventGetIntegerValueField(routed, kCGMouseEventWindowUnderMousePointerThatCanHandleThisEvent) != 123)) ||
            [NSEvent eventWithCGEvent:routed].windowNumber != 123) {
          fprintf(stderr, "route mismatch: type=%u expected=%u window=%ld\n", CGEventGetType(routed), types[i], (long)[NSEvent eventWithCGEvent:routed].windowNumber);
          return 3;
        }
        int64_t state = CGEventGetIntegerValueField(routed, kCGEventSourceStateID);
        if (state == kCGEventSourceStateCombinedSessionState || state == kCGEventSourceStateHIDSystemState ||
            CGEventGetFlags(routed) != 0) {
          fprintf(stderr, "input state leaked: source=%lld flags=%llu type=%u\n",
              (long long)CGEventGetIntegerValueField(routed, kCGEventSourceStateID),
              (unsigned long long)CGEventGetFlags(routed), types[i]);
          return 4;
        }
        if (types[i] == kCGEventScrollWheel &&
            (CGEventGetIntegerValueField(routed, kCGScrollWheelEventPointDeltaAxis1) != -120 ||
             CGEventGetIntegerValueField(routed, kCGScrollWheelEventPointDeltaAxis2) != 15 ||
             CGEventGetIntegerValueField(routed, kCGScrollWheelEventIsContinuous) != 1 ||
             CGEventGetDoubleValueField(routed, kCGScrollWheelEventFixedPtDeltaAxis1) != scrollY)) return 5;
        CFRelease(routed);
      }
    }
    return 0;
  }
}
