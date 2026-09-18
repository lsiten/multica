#import "native-fence-overrides.h"
Boolean ReviewTrusted(void) { return true; }
CFDictionaryRef ReviewSession(void) { return CFBridgingRetain(@{(id)kCGSessionOnConsoleKey:@YES,(id)kCGSessionLoginDoneKey:@YES,(id)kCGSessionUserIDKey:@(getuid()),@"CGSSessionScreenIsLocked":@NO}); }
boolean_t ReviewOnline(CGDirectDisplayID i) { return true; }
CGRect ReviewDisplayBounds(CGDirectDisplayID i) { return CGRectMake(0,0,1600,900); }
uint32_t ReviewVendor(CGDirectDisplayID i) { return 0x4D55; }
uint32_t ReviewModel(CGDirectDisplayID i) { return 1; }
CGRect ACRect(NSDictionary *d) { return CGRectMake([d[@"X"] doubleValue],[d[@"Y"] doubleValue],[d[@"Width"] doubleValue],[d[@"Height"] doubleValue]); }
BOOL ACContains(CGRect a, CGRect b) { return CGRectContainsRect(a,b); }
NSString *ACReadWindow(ACWindow *w, CGRect *r) { *r=w.lastBounds; return nil; }
static int reviewWindows=1;
id ACCopy(AXUIElementRef e, CFStringRef a, ACRequest *r) { return reviewWindows==1?@[@"only-window"]:@[@"one",@"two"]; }

#define STUB(name) NSDictionary *name(ACSession *s, ACRequest *r, NSDictionary *i, NSString **e) { *e=@"probe_unexpected"; return nil; }
STUB(ACObserveDisplay) STUB(ACLaunch) STUB(ACMove) STUB(ACRestore) STUB(ACObserve) STUB(ACAction) STUB(ACAdoptWindow) STUB(ACManagedWindows)
NSDictionary *ACListApps(ACRequest *r, NSString **e) {*e=@"probe_unexpected";return nil;}
NSDictionary *ACListWindows(ACSession *s, ACRequest *r, NSString **e) {*e=@"probe_unexpected";return nil;}
NSDictionary *ACPIDProcess(pid_t pid) {return @{@"PID":@(pid),@"Start":@"1:0",@"UID":@501,@"BundleID":@"synthetic",@"OSBuild":@"test"};}
NSString *ACInputSourceID(void) {return @"synthetic-layout";}
#include "guard-source.inc"

static int reviewLookupMode=0;
static CGEventSourceStateID reviewPostedSource=99, reviewRequestedSource=99, reviewCreatedSource=99;
static CGEventSourceRef reviewSource;
static BOOL reviewScrollUsesSource;
static pid_t reviewPostedPID;
#undef CGEventSourceCreate
#undef CGEventCreateScrollWheelEvent
CGEventSourceRef ReviewSourceCreate(CGEventSourceStateID state) {
 reviewRequestedSource=state;
 reviewSource=CGEventSourceCreate(state);
 reviewCreatedSource=CGEventSourceGetSourceStateID(reviewSource);
 return reviewSource;
}
CGEventRef ReviewScroll(CGEventSourceRef source, CGScrollEventUnit units, uint32_t count, int32_t y, ...) {
 va_list args; va_start(args,y); int32_t x=va_arg(args,int32_t); va_end(args);
 reviewScrollUsesSource=source && source==reviewSource;
 return CGEventCreateScrollWheelEvent(source,units,count,y,x);
}
Boolean ReviewPostAccess(void) { return true; }
void ReviewPost(pid_t pid, CGEventRef e) { reviewPostedPID=pid; CGEventSourceRef source=CGEventCreateSourceFromEvent(e); reviewPostedSource=CGEventSourceGetSourceStateID(source); CFRelease(source); }
NSDictionary *ACProcess(pid_t p) { return nil; }
int ReviewProc(pid_t p,int f,uint64_t a,void *b,int z) { if(reviewLookupMode==2) { struct proc_bsdinfo *info=b; memset(info,0,z); info->pbi_start_tvsec=99; return z; } return 0; }
int ReviewKill(pid_t p,int s) { errno=reviewLookupMode==1?ESRCH:EPERM; return -1; }
#define proc_pidinfo ReviewProc
#define kill ReviewKill
#include "ended-source.inc"
int main(void) { @autoreleasepool {
 ACSession *s=[ACSession new]; s.windows=[NSMutableDictionary new]; s.blockedPIDs=[NSMutableSet new]; s.lock=[NSLock new]; s.pending=dispatch_group_create(); s.pressed=[NSMutableDictionary new]; s.uncertainInput=[NSMutableDictionary new];
 NSDictionary *owner=@{@"RuntimeID":@"A"}, *other=@{@"RuntimeID":@"B"}, *process=@{@"PID":@100001,@"Start":@"1:0",@"UID":@501,@"BundleID":@"synthetic",@"OSBuild":@"test"}; s.uncertainInput[@"fence"]=@{@"Process":process,@"Resource":owner};
 BOOL unknownBlocked=ACInputQuiescent(s,owner)!=nil, siblingClear=ACInputQuiescent(s,other)==nil;
 reviewLookupMode=1; BOOL exitedClear=ACInputQuiescent(s,owner)==nil;
 s.uncertainInput[@"fence"]=@{@"Process":process,@"Resource":owner}; reviewLookupMode=2; BOOL reusedClear=ACInputQuiescent(s,owner)==nil;
 ACWindow *w=[ACWindow new]; w.handle=@"owned";w.windowID=7;w.displayID=1;w.revision=1;w.snapshotValid=YES;w.process=process; w.certifiedProcess=process; w.resource=owner; w.frameWidth=500; w.frameHeight=400; w.lastBounds=CGRectMake(20,20,500,400);
 NSDictionary *d=@{@"ID":@1,@"Bounds":@{@"X":@0,@"Y":@0,@"Width":@1600,@"Height":@900},@"Virtual":@YES}; NSDictionary *epoch=@{@"native_epoch":@"native",@"display_generation":@"display",@"geometry_revision":@1};
 NSMutableDictionary *bound=[d mutableCopy];bound[@"Resource"]=owner;bound[@"Epoch"]=epoch;d=bound;
 w.completionContext=@{@"Token":@"0123456789abcdef",@"Target":@{@"resource":owner,@"epoch":epoch,@"task_id":@"task",@"transaction_id":@"tx",@"lease_epoch":@1,@"window_handle":@"owned",@"snapshot_revision":@1},@"ActionID":@"scroll",@"Sequence":@1,@"Process":process,@"WindowID":@7};
  uintptr_t handle=ac_request_new(3); ACRequest *r=(__bridge ACRequest *)(void *)handle;
 reviewWindows=2; BOOL multiwindowBlocked=ACGuard(s,w,d,r,YES)!=nil; reviewWindows=1;
 NSString *scrollError=ACPIDAction(s,r,w,d,@{@"kind":@"scroll",@"scroll":@{@"position":@{@"x":@10,@"y":@10},@"delta_y":@4,@"delta_x":@0}});
 NSDictionary *result=@{@"boundary":@"original input_darwin.m and exact ACProcessEnded/ACGuard with deterministic OS readback and intercepted post; no events delivered",@"unknown_lookup_retains_fence":@(unknownBlocked),@"other_resource_clear":@(siblingClear),@"confirmed_exit_clears":@(exitedClear),@"start_reuse_clears":@(reusedClear),@"multiple_windows_blocked":@(multiwindowBlocked),@"scroll_error":scrollError?:@"",@"scroll_event_source_state":@(reviewPostedSource),@"requested_source_state":@(reviewRequestedSource),@"created_source_state":@(reviewCreatedSource),@"scroll_uses_created_source":@(reviewScrollUsesSource),@"posted_pid":@(reviewPostedPID)};
 NSData *json=[NSJSONSerialization dataWithJSONObject:result options:NSJSONWritingPrettyPrinted error:nil]; fwrite(json.bytes,1,json.length,stdout); puts(""); ac_request_free(handle);
 return unknownBlocked&&siblingClear&&exitedClear&&reusedClear&&multiwindowBlocked&&scrollError==nil&&reviewRequestedSource==kCGEventSourceStatePrivate&&reviewScrollUsesSource&&reviewPostedSource==reviewCreatedSource&&reviewPostedPID==100001 ? 0:1;
}}
