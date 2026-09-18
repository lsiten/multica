#import "native_internal.h"
Boolean ReviewTrusted(void);
CFDictionaryRef ReviewSession(void);
boolean_t ReviewOnline(CGDirectDisplayID);
CGRect ReviewDisplayBounds(CGDirectDisplayID);
uint32_t ReviewVendor(CGDirectDisplayID);
uint32_t ReviewModel(CGDirectDisplayID);
#define AXIsProcessTrusted ReviewTrusted
#define CGSessionCopyCurrentDictionary ReviewSession
#define CGDisplayIsOnline ReviewOnline
#define CGDisplayBounds ReviewDisplayBounds
#define CGDisplayVendorNumber ReviewVendor
#define CGDisplayModelNumber ReviewModel

Boolean ReviewPostAccess(void);
void ReviewPost(pid_t, CGEventRef);
#define CGPreflightPostEventAccess ReviewPostAccess
#define CGEventPostToPid ReviewPost

CGEventSourceRef ReviewSourceCreate(CGEventSourceStateID);
CGEventRef ReviewScroll(CGEventSourceRef, CGScrollEventUnit, uint32_t, int32_t, ...);
#define CGEventSourceCreate ReviewSourceCreate
#define CGEventCreateScrollWheelEvent ReviewScroll
