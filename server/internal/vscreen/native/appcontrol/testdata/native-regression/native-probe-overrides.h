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
