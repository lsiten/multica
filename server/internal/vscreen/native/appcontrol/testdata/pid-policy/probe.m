#import "native_internal.h"
#import <Security/Security.h>
#import <Carbon/Carbon.h>

@implementation ACWindow
@end
@implementation ACSession
@end
@implementation ACRequest
@end
@implementation ACPressed
@end

static NSDictionary *signingInfo;
static OSStatus signatureStatus;
static NSDictionary *liveProcess;
static NSString *liveSource;
static int nativeGuards, posts, releases;
static int FakePath(pid_t pid, void *buffer, uint32_t size) {
  snprintf(buffer, size, "/synthetic/App.app/Contents/MacOS/App"); return 45;
}
static OSStatus FakeGuest(SecCodeRef host, CFDictionaryRef attributes, SecCSFlags flags, SecCodeRef *code) {
  *code=(SecCodeRef)CFBridgingRetain(@{@"synthetic":@YES}); return errSecSuccess;
}
static OSStatus FakeValidity(SecCodeRef code, SecCSFlags flags, SecRequirementRef requirement) {return signatureStatus;}
static OSStatus FakeInfo(SecStaticCodeRef code, SecCSFlags flags, CFDictionaryRef *out) {
  *out=(CFDictionaryRef)CFBridgingRetain(signingInfo); return errSecSuccess;
}
#define proc_pidpath FakePath
#define SecCodeCopyGuestWithAttributes FakeGuest
#define SecCodeCheckValidity FakeValidity
#define SecCodeCopySigningInformation FakeInfo
#include "executable-source.inc"

NSDictionary *ACPIDProcess(pid_t pid) {return liveProcess;}
NSString *ACInputSourceID(void) {return liveSource;}
NSString *ACGuard(ACSession *s, ACWindow *w, NSDictionary *d, ACRequest *r, BOOL background) {nativeGuards++;return @"probe-stop-before-input";}
NSString *ACReadWindow(ACWindow *w, CGRect *bounds) {*bounds=w.lastBounds;return nil;}
CGRect ACRect(NSDictionary *d) {return CGRectZero;}
BOOL ACContains(CGRect d, CGRect w) {return YES;}
BOOL ACExpired(ACRequest *r) {return NO;}
static CGEventType FakeType(CGEventRef event) {return kCGEventKeyDown;}
static void FakePost(pid_t pid, CGEventRef event) {posts++;}
BOOL ACProcessEnded(NSDictionary *p) {return NO;}
static Boolean FakePostAccess(void) {return true;}
static void FakeRelease(CFTypeRef value) {releases++;CFRelease(value);}
#define CFRelease FakeRelease
#define CGEventGetType FakeType
#define CGEventPostToPid FakePost
#define CGPreflightPostEventAccess FakePostAccess
#include "press-key-source.inc"
#include "post-source.inc"
#include "release-source.inc"

int main(void) { @autoreleasepool {
  NSString *path=@"/synthetic/App.app/Contents/MacOS/App";
  NSMutableDictionary *info=[@{(__bridge id)kSecCodeInfoIdentifier:@"synthetic.app",(__bridge id)kSecCodeInfoUnique:[NSMutableData dataWithLength:20],(__bridge id)kSecCodeInfoMainExecutable:[NSURL fileURLWithPath:path],(__bridge id)kSecCodeInfoPList:@{@"CFBundleIdentifier":@"synthetic.app",@"CFBundleShortVersionString":@"1",@"CFBundleVersion":@"7"}} mutableCopy];
  signingInfo=info;signatureStatus=errSecSuccess;
  NSDictionary *valid=ACExecutableIdentity(1,@"synthetic.app");
  BOOL validRead=[valid[@"ExecutablePath"] isEqual:path] && [valid[@"AppVersion"] isEqual:@"1"] && [valid[@"CodeHash"] length]==40;
  signatureStatus=errSecCSUnsigned;BOOL unsignedDenied=ACExecutableIdentity(1,@"synthetic.app").count==0;signatureStatus=errSecSuccess;
  info[(__bridge id)kSecCodeInfoMainExecutable]=[NSURL fileURLWithPath:@"/other/executable"];
  BOOL pathDenied=ACExecutableIdentity(1,@"synthetic.app").count==0;info[(__bridge id)kSecCodeInfoMainExecutable]=[NSURL fileURLWithPath:path];
  BOOL bundleDenied=ACExecutableIdentity(1,@"forged.app").count==0;
  [info removeObjectForKey:(__bridge id)kSecCodeInfoIdentifier];BOOL noSignatureDenied=ACExecutableIdentity(1,@"synthetic.app").count==0;
  ACWindow *w=[ACWindow new];w.process=@{@"PID":@1};w.certifiedProcess=valid;w.certifiedInputSource=@"source-A";
  liveProcess=valid;liveSource=@"source-B";
  BOOL sourceDenied=[post(nil,nil,w,@{},(CGEventRef)CFBridgingRetain(@"synthetic-event")) isEqual:@"needs_intervention"] && nativeGuards==0;
  liveSource=@"source-A";liveProcess=@{@"CodeHash":@"replaced"};
  BOOL replacedDenied=[post(nil,nil,w,@{},(CGEventRef)CFBridgingRetain(@"synthetic-event")) isEqual:@"stale_window"] && nativeGuards==0;
  liveProcess=valid;
  BOOL matchingReachedGuard=[post(nil,nil,w,@{},(CGEventRef)CFBridgingRetain(@"synthetic-event")) isEqual:@"probe-stop-before-input"] && nativeGuards==1 && posts==0 && releases==3;
  ACSession *session=[ACSession new];session.pressed=[NSMutableDictionary new];session.uncertainInput=[NSMutableDictionary new];
  ACPressed *pressed=[ACPressed new];pressed.process=w.process;pressed.certifiedProcess=valid;pressed.releaseEvent=@"synthetic-release";
  session.pressed[@"test"]=pressed;liveProcess=@{@"CodeHash":@"replaced"};releaseInterrupted(session,w);
  BOOL changedReleaseDenied=posts==0 && session.uncertainInput.count==1;
  session.pressed[@"test"]=pressed;liveProcess=valid;releaseInterrupted(session,w);
  BOOL matchedReleaseAttempted=posts==1 && session.uncertainInput.count==1;
  NSDictionary *result=@{@"boundary":@"exact production executable identity and PID pre-dispatch code; injected OS metadata, no GUI or delivered events",@"valid_signed_identity":@(validRead),@"unsigned_denied":@(unsignedDenied),@"path_mismatch_denied":@(pathDenied),@"bundle_mismatch_denied":@(bundleDenied),@"missing_signing_id_denied":@(noSignatureDenied),@"changed_input_source_denied":@(sourceDenied),@"changed_code_identity_denied":@(replacedDenied),@"matching_context_reaches_existing_guard":@(matchingReachedGuard),@"delivered_events":@0,@"intercepted_release_attempts":@(posts),@"changed_code_cleanup_denied":@(changedReleaseDenied),@"matching_code_cleanup_still_uncertain":@(matchedReleaseAttempted),@"released_events":@(releases)};
  NSData *data=[NSJSONSerialization dataWithJSONObject:result options:NSJSONWritingPrettyPrinted error:nil];fwrite(data.bytes,1,data.length,stdout);puts("");
  return validRead&&unsignedDenied&&pathDenied&&bundleDenied&&noSignatureDenied&&sourceDenied&&replacedDenied&&matchingReachedGuard&&changedReleaseDenied&&matchedReleaseAttempted?0:1;
} }
