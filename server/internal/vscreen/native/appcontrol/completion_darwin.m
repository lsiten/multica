//go:build darwin && cgo

#import "native_internal.h"

uint64_t ACPIDCompletionToken(NSDictionary *context) {
  id token=context[@"Token"];
  if (![token isKindOfClass:NSString.class] || [token length]!=16)
    return 0;
  uint64_t value=0;
  for (NSUInteger i=0;i<16;i++) {
    unichar c=[token characterAtIndex:i];
    if (!((c>='0'&&c<='9')||(c>='a'&&c<='f')))
      return 0;
    value=(value<<4)|(c<='9'?c-'0':c-'a'+10);
  }
  return value;
}
static BOOL completionMatchesWindow(NSDictionary *context,ACWindow *w,NSDictionary *display) {
  NSDictionary *target=context[@"Target"],*process=context[@"Process"];
  if (!w || !ACPIDCompletionToken(context) || ![target isKindOfClass:NSDictionary.class] ||
      ![process isKindOfClass:NSDictionary.class] || ![target[@"window_handle"] isEqual:w.handle] ||
      ![target[@"resource"] isEqual:display[@"Resource"]] || ![target[@"epoch"] isEqual:display[@"Epoch"]] ||
      ![w.resource isEqual:display[@"Resource"]] || [context[@"WindowID"] unsignedIntValue]!=w.windowID ||
      ![target[@"task_id"] isKindOfClass:NSString.class] || ![target[@"task_id"] length] ||
      ![target[@"transaction_id"] isKindOfClass:NSString.class] || ![target[@"transaction_id"] length] ||
      ![target[@"lease_epoch"] unsignedLongLongValue] || ![context[@"Sequence"] unsignedLongLongValue] ||
      ![context[@"ActionID"] isKindOfClass:NSString.class] || ![context[@"ActionID"] length])
    return NO;
  for (NSString *key in @[@"PID",@"UID",@"Start",@"BundleID",@"OSBuild"])
    if (!process[key] || ![process[key] isEqual:w.process[key]])
      return NO;
  return YES;
}
NSString *ACPreparePIDCompletion(ACSession *s,ACWindow *w,NSDictionary *display,ACRequest *request) {
  NSDictionary *context=w.completionContext;
  if (ACExpired(request) || !w.snapshotValid || !completionMatchesWindow(context,w,display) || w.displayID!=[display[@"ID"] unsignedIntValue] ||
      [context[@"Target"][@"snapshot_revision"] unsignedLongLongValue]!=w.revision)
    return @"needs_intervention";
  NSString *pending=ACInputQuiescent(s,display[@"Resource"]);
  if (pending)
    return pending;
  if ([s.usedPIDTokens containsObject:context[@"Token"]] || s.usedPIDTokens.count>=4096)
    return @"action_uncertain";
  return nil;
}
void ACRegisterPIDCompletion(ACSession *s,ACWindow *w) {
  if (!s.pendingPIDInputs) s.pendingPIDInputs=[NSMutableDictionary new];
  if (!s.usedPIDTokens) s.usedPIDTokens=[NSMutableSet new];
  NSDictionary *context=w.completionContext;
  [s.usedPIDTokens addObject:context[@"Token"]];
  s.pendingPIDInputs[context[@"Token"]]=@{@"Context":[context copy],@"Process":w.process,@"Resource":w.resource,@"CertifiedProcess":[w.certifiedProcess copy]};
}
NSDictionary *ACCompletePIDInput(ACSession *s,ACRequest *r,NSDictionary *input,NSString **error) {
  NSDictionary *context=input[@"Completion"],*display=input[@"Display"];
  if (!ACPIDCompletionToken(context)) {*error=@"action_uncertain";return nil;}
  NSDictionary *pending=s.pendingPIDInputs[context[@"Token"]];
  ACWindow *w=s.windows[context[@"Target"][@"window_handle"]];
  if (!pending || ![pending[@"Context"] isEqual:context] || !completionMatchesWindow(context,w,display) ||
      ACExpired(r) || ![ACProcess([w.process[@"PID"] intValue]) isEqual:w.process] ||
      ![ACPIDProcess([w.process[@"PID"] intValue]) isEqual:pending[@"CertifiedProcess"]]) {
    *error=@"action_uncertain";return nil;
  }
  for (ACPressed *pressed in s.pressed.allValues)
    if ([pressed.process isEqual:w.process]) {*error=@"action_uncertain";return nil;}
  *error=ACGuard(s,w,display,r,YES);
  CGRect bounds;
  if (!*error) *error=ACReadWindow(w,&bounds);
  if (!*error && (w.displayID!=[display[@"ID"] unsignedIntValue] || !CGRectEqualToRect(bounds,w.lastBounds) || !ACContains(ACRect(display[@"Bounds"]),bounds))) *error=@"stale_window";
  if (*error || ACExpired(r)) {if(!*error)*error=@"action_uncertain";return nil;}
  [s.pendingPIDInputs removeObjectForKey:context[@"Token"]];
  return @{};
}
