#import "bridge.h"
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <ImageIO/ImageIO.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#include <errno.h>
#include <libproc.h>
#include <signal.h>
#include <sys/sysctl.h>
#include <time.h>

@interface ACRequest : NSObject
@property(atomic) BOOL cancelled;
@property double deadline;
@end
@interface ACWindow : NSObject
@property(strong) id element;
@property(strong) NSDictionary *process;
@property(strong) NSDictionary *resource;
@property(strong) NSMutableDictionary<NSString *, id> *elements;
@property(strong) NSString *handle;
@property(strong) NSString *certifiedInputSource;
@property(strong) NSDictionary *certifiedProcess;
@property(strong) NSDictionary *completionContext;
@property BOOL pidDispatched;
@property CGRect original;
@property CGRect lastBounds;
@property uint32_t windowID;
@property uint32_t displayID;
@property uint64_t revision;
@property BOOL snapshotValid;
@property uint64_t sessionGeneration;
@property uint32_t frameWidth;
@property uint32_t frameHeight;
@property BOOL moved;
@end
@interface ACPressed : NSObject
@property(strong) NSDictionary *process;
@property(strong) NSDictionary *certifiedProcess;
@property(strong) NSDictionary *resource;
@property(strong) id releaseEvent;
@end
@interface ACSession : NSObject
@property(strong) NSMutableDictionary<NSString *, NSDictionary *> *pendingPIDInputs;
@property(strong) NSMutableSet<NSString *> *usedPIDTokens;
@property(strong) NSMutableDictionary<NSString *, ACWindow *> *windows;
@property(strong) NSMutableSet<NSNumber *> *blockedPIDs;
@property(strong) NSLock *lock;
@property(strong) NSMutableDictionary<NSString *, ACPressed *> *pressed;
@property(strong)
    NSMutableDictionary<NSString *, NSDictionary *> *uncertainInput;
@property(strong) dispatch_group_t pending;
@property(strong) id activationObserver;
@property(strong) id sessionObserver;
@property(strong) id sleepObserver;
@property(strong) id lockObserver;
@property BOOL sessionBlocked;
@property uint64_t generation;
@end

double ACNow(void);
BOOL ACExpired(ACRequest *);
CGRect ACRect(NSDictionary *);
NSDictionary *ACBounds(CGRect);
BOOL ACContains(CGRect, CGRect);
NSDictionary *ACProcess(pid_t);
BOOL ACProcessEnded(NSDictionary *);
NSString *ACReadWindow(ACWindow *, CGRect *);
NSString *ACGuard(ACSession *, ACWindow *, NSDictionary *, ACRequest *, BOOL);
NSDictionary *ACWindowValue(ACWindow *);
id ACCopy(AXUIElementRef, CFStringRef, ACRequest *);
CGRect ACElementBounds(AXUIElementRef, ACRequest *);
NSDictionary *ACLaunch(ACSession *, ACRequest *, NSDictionary *, NSString **);
NSDictionary *ACRestore(ACSession *, ACRequest *, NSDictionary *, NSString **);
NSDictionary *ACMove(ACSession *, ACRequest *, NSDictionary *, NSString **);
NSDictionary *ACObserve(ACSession *, ACRequest *, NSDictionary *, NSString **);
NSDictionary *ACAction(ACSession *, ACRequest *, NSDictionary *, NSString **);
void ACMarkUncertain(ACSession *, ACWindow *, NSString *);
NSString *ACInputQuiescent(ACSession *, NSDictionary *);
NSString *ACPIDAction(ACSession *, ACRequest *, ACWindow *, NSDictionary *,
                      NSDictionary *);
NSDictionary *ACObserveDisplay(ACSession *, ACRequest *, NSDictionary *,
                               NSString **);
NSData *ACScreenshot(ACSession *, ACRequest *, ACWindow *, NSDictionary *,
                     NSString **);

NSDictionary *ACListApps(ACRequest *r, NSString **error);

NSDictionary *ACListWindows(ACSession *, ACRequest *, NSString **);
NSDictionary *ACAdoptWindow(ACSession *, ACRequest *, NSDictionary *, NSString **);

NSDictionary *ACManagedWindows(ACSession *s, ACRequest *r, NSDictionary *display, NSString **error);
NSString *ACInputSourceID(void);

NSDictionary *ACPIDProcess(pid_t);
NSDictionary *ACManagedWindows(ACSession *s, ACRequest *r, NSDictionary *display, NSString **error);

uint64_t ACPIDCompletionToken(NSDictionary *);
NSString *ACPreparePIDCompletion(ACSession *,ACWindow *,NSDictionary *,ACRequest *);
void ACRegisterPIDCompletion(ACSession *,ACWindow *);
NSDictionary *ACCompletePIDInput(ACSession *,ACRequest *,NSDictionary *,NSString **);
