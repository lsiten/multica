#import "overrides.h"
@implementation ACRequest
@end
@implementation ACWindow
@end
@implementation ACPressed
@end
@implementation ACSession
@end
static int posts,processed;
static uint64_t finalToken;
static BOOL ended;
static NSDictionary *process,*richProcess;
Boolean CompletionProbeAccess(void){return true;}
void CompletionProbePost(pid_t pid,CGEventRef event){posts++;CGEventType type=CGEventGetType(event);if(type==kCGEventLeftMouseUp || type==kCGEventKeyUp || type==kCGEventScrollWheel)finalToken=(uint64_t)CGEventGetIntegerValueField(event,kCGEventSourceUserData);}
NSDictionary *ACProcess(pid_t pid){return process;}
NSDictionary *ACPIDProcess(pid_t pid){return richProcess;}
NSString *ACInputSourceID(void){return @"synthetic-layout";}
BOOL ACProcessEnded(NSDictionary *p){return ended;}
double ACNow(void){struct timespec ts;clock_gettime(CLOCK_MONOTONIC,&ts);return ts.tv_sec+ts.tv_nsec/1e9;}
BOOL ACExpired(ACRequest *r){return r.cancelled || ACNow()>=r.deadline;}
NSString *ACGuard(ACSession *s,ACWindow *w,NSDictionary *d,ACRequest *r,BOOL background){return ACExpired(r)?@"action_uncertain":nil;}
NSString *ACReadWindow(ACWindow *w,CGRect *bounds){*bounds=w.lastBounds;return nil;}
CGRect ACRect(NSDictionary *d){return CGRectMake([d[@"X"] doubleValue],[d[@"Y"] doubleValue],[d[@"Width"] doubleValue],[d[@"Height"] doubleValue]);}
BOOL ACContains(CGRect d,CGRect w){return CGRectContainsRect(d,w);}
int main(void){@autoreleasepool{
 ACSession *s=[ACSession new];s.pressed=[NSMutableDictionary new];s.uncertainInput=[NSMutableDictionary new];s.windows=[NSMutableDictionary new];s.lock=[NSLock new];
 NSDictionary *resource=@{@"backend_identity":@"https://fixture.invalid",@"workspace_id":@"workspace",@"runtime_id":@"runtime",@"uid":@501};
 NSDictionary *epoch=@{@"native_epoch":@"native",@"display_generation":@"display",@"geometry_revision":@1};
 process=@{@"PID":@100001,@"UID":@501,@"Start":@"10:0",@"BundleID":@"synthetic",@"OSBuild":@"test"};
 richProcess=[process mutableCopy];[(NSMutableDictionary *)richProcess setObject:@"original-code" forKey:@"CodeHash"];
 ACWindow *w=[ACWindow new];w.handle=@"owned";w.process=process;w.certifiedProcess=richProcess;w.certifiedInputSource=@"synthetic-layout";w.resource=resource;w.windowID=7;w.displayID=42;w.revision=1;w.snapshotValid=YES;w.frameWidth=500;w.frameHeight=400;w.lastBounds=CGRectMake(20,20,500,400);s.windows[w.handle]=w;
 NSDictionary *d=@{@"ID":@42,@"Bounds":@{@"X":@0,@"Y":@0,@"Width":@1600,@"Height":@900},@"Virtual":@YES,@"Resource":resource,@"Epoch":epoch};
 NSDictionary *binding=@{@"Token":@"0123456789abcdef",@"Target":@{@"resource":resource,@"epoch":epoch,@"task_id":@"task",@"transaction_id":@"tx",@"lease_epoch":@1,@"window_handle":@"owned",@"snapshot_revision":@1},@"ActionID":@"click",@"Sequence":@1,@"Process":process,@"WindowID":@7};
 if([w respondsToSelector:@selector(setCompletionContext:)])[w setValue:binding forKey:@"completionContext"];
 ACRequest *r=[ACRequest new];r.deadline=ACNow()+3;
 NSString *error=ACPIDAction(s,r,w,d,@{@"kind":@"click",@"click":@{@"position":@{@"x":@10,@"y":@10}}});
 NSString *quiescence=ACInputQuiescent(s,resource);
 BOOL blocked=[quiescence isEqual:@"action_uncertain"];
 BOOL pairBlocked=blocked && posts==2 && processed==0 && s.pressed.count==0 && finalToken==0x0123456789abcdefULL;
 BOOL wrongAckBlocked=YES,identityBlocked=YES,cancelBlocked=YES,clearConfirmed=YES,replayBlocked=YES,scrollBlocked=YES,exitClears=YES,missingContextBlocked=YES,siblingClear=YES;
 if ([s respondsToSelector:@selector(pendingPIDInputs)]) {
  NSString *clearError=nil;
  NSMutableDictionary *wrong=[binding mutableCopy];wrong[@"ActionID"]=@"foreign";
  ACCompletePIDInput(s,r,@{@"Completion":wrong,@"Display":d},&clearError);
  wrongAckBlocked=clearError!=nil && ACInputQuiescent(s,resource)!=nil;
  NSDictionary *original=richProcess;richProcess=@{@"CodeHash":@"replaced"};clearError=nil;
  ACCompletePIDInput(s,r,@{@"Completion":binding,@"Display":d},&clearError);
  identityBlocked=clearError!=nil && ACInputQuiescent(s,resource)!=nil;richProcess=original;
  r.cancelled=YES;clearError=nil;ACCompletePIDInput(s,r,@{@"Completion":binding,@"Display":d},&clearError);
  cancelBlocked=clearError!=nil && ACInputQuiescent(s,resource)!=nil;r.cancelled=NO;
  processed=1; // Simulate the trusted verifier confirmation before its private clear call.
  clearError=nil;ACCompletePIDInput(s,r,@{@"Completion":binding,@"Display":d},&clearError);
  clearConfirmed=clearError==nil && ACInputQuiescent(s,resource)==nil;
  clearError=nil;ACCompletePIDInput(s,r,@{@"Completion":binding,@"Display":d},&clearError);replayBlocked=clearError!=nil;
  NSMutableDictionary *scroll=[binding mutableCopy];scroll[@"Token"]=@"fedcba9876543210";scroll[@"ActionID"]=@"scroll";scroll[@"Sequence"]=@2;
  [w setValue:scroll forKey:@"completionContext"];
  NSString *scrollError=ACPIDAction(s,r,w,d,@{@"kind":@"scroll",@"scroll":@{@"position":@{@"x":@10,@"y":@10},@"delta_y":@1,@"delta_x":@0}});
  scrollBlocked=scrollError==nil && ACInputQuiescent(s,resource)!=nil && finalToken==0xfedcba9876543210ULL;
  NSMutableDictionary *sibling=[resource mutableCopy];sibling[@"runtime_id"]=@"sibling";siblingClear=ACInputQuiescent(s,sibling)==nil;
  int before=posts;[w setValue:nil forKey:@"completionContext"];
  missingContextBlocked=ACPIDAction(s,r,w,d,@{@"kind":@"scroll",@"scroll":@{@"position":@{@"x":@10,@"y":@10},@"delta_y":@1}})!=nil && posts==before;
  ended=YES;exitClears=ACInputQuiescent(s,resource)==nil;
 }
 NSDictionary *result=@{@"post_error":error?:@"",@"post_calls":@(posts),@"target_processed_before_confirmation":@0,@"synthetic_target_confirmation":@(processed==1),@"pending_blocks_after_complete_post_pair":@(pairBlocked),@"wrong_action_ack_blocked":@(wrongAckBlocked),@"rich_identity_change_blocked":@(identityBlocked),@"cancelled_clear_blocked":@(cancelBlocked),@"trusted_exact_clear_allowed":@(clearConfirmed),@"clear_replay_blocked":@(replayBlocked),@"scroll_pending_and_full_width_token":@(scrollBlocked),@"confirmed_process_exit_clears":@(exitClears),@"missing_completion_context_denies_before_post":@(missingContextBlocked),@"sibling_resource_not_blocked":@(siblingClear),@"boundary":@"production PID/clear code; all event posts intercepted and never delivered"};
 NSData *data=[NSJSONSerialization dataWithJSONObject:result options:NSJSONWritingPrettyPrinted error:nil];fwrite(data.bytes,1,data.length,stdout);puts("");
 return error!=nil || !pairBlocked || !wrongAckBlocked || !identityBlocked || !cancelBlocked || !clearConfirmed || !replayBlocked || !scrollBlocked || !exitClears || !missingContextBlocked || !siblingClear;
}}
