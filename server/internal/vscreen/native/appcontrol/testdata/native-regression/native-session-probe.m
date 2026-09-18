#import "native-probe-overrides.h"
Boolean ReviewTrusted(void) { return true; }
CFDictionaryRef ReviewSession(void) { return CFBridgingRetain(@{(id)kCGSessionOnConsoleKey:@YES,(id)kCGSessionLoginDoneKey:@YES,(id)kCGSessionUserIDKey:@(getuid()),@"CGSSessionScreenIsLocked":@NO}); }
boolean_t ReviewOnline(CGDirectDisplayID i) { return true; }
CGRect ReviewDisplayBounds(CGDirectDisplayID i) { return CGRectMake(0,0,1600,900); }
uint32_t ReviewVendor(CGDirectDisplayID i) { return 0x4D55; }
uint32_t ReviewModel(CGDirectDisplayID i) { return 1; }
CGRect ACRect(NSDictionary *d) { return CGRectMake([d[@"X"] doubleValue],[d[@"Y"] doubleValue],[d[@"Width"] doubleValue],[d[@"Height"] doubleValue]); }
BOOL ACContains(CGRect a, CGRect b) { return CGRectContainsRect(a,b); }
static ACSession *reviewSession;
static BOOL invalidateDuringRead;
NSString *ACReadWindow(ACWindow *w, CGRect *r) {
 if (invalidateDuringRead) {reviewSession.generation++; reviewSession.sessionBlocked=YES; invalidateDuringRead=NO;}
 *r=w.lastBounds; return nil;
}
id ACCopy(AXUIElementRef e, CFStringRef a, ACRequest *r) { return @[@"only-window"]; }
NSString *ACInputQuiescent(ACSession *s, NSDictionary *r) { return nil; }
#define STUB(name) NSDictionary *name(ACSession *s, ACRequest *r, NSDictionary *i, NSString **e) { *e=@"probe_unexpected"; return nil; }
STUB(ACObserveDisplay) STUB(ACLaunch) STUB(ACMove) STUB(ACRestore) STUB(ACObserve) STUB(ACAction) STUB(ACAdoptWindow) STUB(ACManagedWindows)
NSDictionary *ACListApps(ACRequest *r, NSString **e) {*e=@"probe_unexpected";return nil;}
NSDictionary *ACListWindows(ACSession *s, ACRequest *r, NSString **e) {*e=@"probe_unexpected";return nil;}
NSDictionary *ACPIDProcess(pid_t pid) {return @{@"PID":@(pid),@"Start":@"1:0"};}
NSString *ACInputSourceID(void) {return @"synthetic-layout";}
#include "guard-source.inc"
static NSString *resume(ACSession *s, NSDictionary *display, uintptr_t r) {
 NSData *raw=[NSJSONSerialization dataWithJSONObject:@{@"Operation":@"resume",@"Input":display} options:0 error:nil]; char *out=NULL; size_t size=0;
 int status=ac_call((uintptr_t)(__bridge void *)s,r,raw.bytes,raw.length,&out,&size);
 if(status) return @"transport_failure";
 NSDictionary *reply=[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:out length:size] options:0 error:nil]; free(out); return reply[@"Error"];
}
int main(void) { @autoreleasepool {
 ACSession *s=[ACSession new]; reviewSession=s; s.windows=[NSMutableDictionary new]; s.blockedPIDs=[NSMutableSet new]; s.lock=[NSLock new]; s.pending=dispatch_group_create(); s.generation=1; s.sessionBlocked=YES;
 for (int i=1;i<=3;i++) {ACWindow *w=[ACWindow new];w.process=@{@"PID":@(100000+i)};w.displayID=i==3?1:i;w.lastBounds=CGRectMake(20,20,500,400);s.windows[@(i).stringValue]=w;}
 NSDictionary *bounds=@{@"X":@0,@"Y":@0,@"Width":@1600,@"Height":@900};
 NSDictionary *a=@{@"ID":@1,@"Bounds":bounds,@"Virtual":@YES}, *b=@{@"ID":@2,@"Bounds":bounds,@"Virtual":@YES};
 uintptr_t r=ac_request_new(3); ACRequest *request=(__bridge ACRequest *)(void *)r;
 s.windows[@"3"].lastBounds=CGRectMake(2000,20,500,400);
 BOOL failedAtomic=[resume(s,a,r) isEqual:@"needs_intervention"] && s.sessionBlocked && s.windows[@"1"].sessionGeneration==0 && s.windows[@"3"].sessionGeneration==0 && [ACGuard(s,s.windows[@"2"],b,request,YES) isEqual:@"needs_intervention"];
 s.windows[@"3"].lastBounds=CGRectMake(20,20,500,400);
 BOOL recoveredA=[resume(s,a,r) isEqual:@""] && ACGuard(s,s.windows[@"1"],a,request,YES)==nil;
 BOOL siblingBlocked=[ACGuard(s,s.windows[@"2"],b,request,YES) isEqual:@"needs_intervention"] && s.windows[@"2"].sessionGeneration==0;
 BOOL recoveredB=[resume(s,b,r) isEqual:@""] && ACGuard(s,s.windows[@"2"],b,request,YES)==nil;
 s.generation++;s.sessionBlocked=YES;invalidateDuringRead=YES;
 BOOL invalidationRejected=[resume(s,a,r) isEqual:@"needs_intervention"] && s.sessionBlocked && s.windows[@"1"].sessionGeneration==1 && s.windows[@"3"].sessionGeneration==1;
 NSDictionary *result=@{@"failed_recovery_atomic":@(failedAtomic),@"own_A_recovery_allowed":@(recoveredA),@"unresumed_B_refused":@(siblingBlocked),@"own_B_recovery_allowed":@(recoveredB),@"invalidation_during_readback_refused":@(invalidationRejected),@"boundary":@"actual bridge and extracted current guard; OS readback stubs; no event delivery"};
 NSData *json=[NSJSONSerialization dataWithJSONObject:result options:NSJSONWritingPrettyPrinted error:nil];fwrite(json.bytes,1,json.length,stdout);puts("");ac_request_free(r);
 return failedAtomic&&recoveredA&&siblingBlocked&&recoveredB&&invalidationRejected?0:1;
}}
