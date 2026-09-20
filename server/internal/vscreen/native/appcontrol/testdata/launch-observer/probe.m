#import <Foundation/Foundation.h>
#import <ApplicationServices/ApplicationServices.h>
@interface ACRequest : NSObject
@property double deadline;
@end
@implementation ACRequest
@end
static int attempts, waits, mode;
static double now;
static BOOL ACExpired(ACRequest *r) {return now >= r.deadline;}
static double ACNow(void) {return now;}
static NSDictionary *ACProcess(pid_t pid) {return mode==2 && waits ? @{@"pid":@2} : @{@"pid":@1};}
static AXError timeout(AXUIElementRef a,float t){return kAXErrorSuccess;}
static AXError add(AXObserverRef o,AXUIElementRef a,CFStringRef n,void *c){attempts++;return mode==3?kAXErrorNotificationUnsupported:(mode==1||mode==2||attempts<2?kAXErrorCannotComplete:kAXErrorSuccess);}
static SInt32 ReviewWait(CFRunLoopMode m,CFTimeInterval t,Boolean b){waits++;now+=t;return 0;}
#define AXUIElementSetMessagingTimeout timeout
#define AXObserverAddNotification add
#define CFRunLoopRunInMode ReviewWait
#include "observer.inc"
int main(void){@autoreleasepool{
 for(mode=0;mode<4;mode++){
  attempts=waits=0;now=0;ACRequest*r=[ACRequest new];r.deadline=.2;
  AXError e=ACRegisterWindowObserver(NULL,NULL,1,@{@"pid":@1},r);
  if(mode==0 && (e!=kAXErrorSuccess||attempts!=2))return 1;
  if(mode==1 && (e==kAXErrorSuccess||now<r.deadline))return 2;
  if(mode==2 && (e==kAXErrorSuccess||attempts!=1))return 3;
  if(mode==3 && (e!=kAXErrorNotificationUnsupported||attempts!=1))return 4;
 }
 return 0;
}}
