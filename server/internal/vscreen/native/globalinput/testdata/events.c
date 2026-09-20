#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>
#include <stdbool.h>
#include <stdio.h>
#include <string.h>

static CGEventRef events[32];
static size_t count;
static void record(CGEventTapLocation tap, CGEventRef event) {
  if (count < 32) events[count++] = CGEventCreateCopy(event);
}
#define CGEventPost record
#include "events.inc"

int main(int argc, char **argv) {
  bool ok = false;
  if (argc != 2) return 2;
  if (!strcmp(argv[1], "unicode")) {
    uint16_t text[] = {0x4E2D, 0xD83D, 0xDE80, 'e', 0x0301};
    gi_unicode(text, 5);
    uint16_t result[32];
    size_t used = 0;
    ok = count > 0 && count % 2 == 0;
    for (size_t i = 0; i < count; i += 2) {
      UniChar chars[16]; UniCharCount length = 0;
      CGEventKeyboardGetUnicodeString(events[i], 16, &length, chars);
      ok &= CGEventGetType(events[i]) == kCGEventKeyDown &&
            CGEventGetType(events[i+1]) == kCGEventKeyUp;
      for (size_t j = 0; j < length; j++) {
        if (chars[j] >= 0xD800 && chars[j] <= 0xDBFF)
          ok &= j+1 < length && chars[j+1] >= 0xDC00 && chars[j+1] <= 0xDFFF;
        if (used < 32) result[used++] = chars[j];
      }
    }
    ok &= used == 5 && !memcmp(result, text, sizeof(text));
  } else if (!strcmp(argv[1], "drag")) {
    gi_mouse(1, 10, 20, 1, 0, 0);
    gi_mouse(1, 10, 20, 2, 0, 0);
    gi_mouse(1, 10, 20, 3, 0, 0);
    gi_mouse(1, 10, 20, 0, 0, 0);
    ok = count == 4 && CGEventGetType(events[0]) == kCGEventLeftMouseDragged &&
      CGEventGetType(events[1]) == kCGEventOtherMouseDragged &&
      CGEventGetType(events[2]) == kCGEventRightMouseDragged &&
      CGEventGetType(events[3]) == kCGEventMouseMoved;
  } else if (!strcmp(argv[1], "wheel")) {
    gi_mouse(4, 123, 456, 0, 3, 4);
    CGPoint p = count ? CGEventGetLocation(events[0]) : CGPointZero;
    ok = count == 1 && p.x == 123 && p.y == 456;
  } else if (!strcmp(argv[1], "modifiers")) {
    gi_key(8, true, kCGEventFlagMaskControl);
    gi_key(8, false, 0);
    ok = count == 2 && CGEventGetFlags(events[0]) == kCGEventFlagMaskControl &&
      CGEventGetFlags(events[1]) == 0;
  }
  for (size_t i = 0; i < count; i++) CFRelease(events[i]);
  if (!ok) fprintf(stderr, "%s event contract failed\n", argv[1]);
  return ok ? 0 : 1;
}
