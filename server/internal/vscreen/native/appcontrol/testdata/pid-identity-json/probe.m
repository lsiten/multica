#import <Foundation/Foundation.h>
#include <sys/types.h>
static NSDictionary *metadata;
NSDictionary *ACProcess(pid_t pid) {
  return @{@"PID":@123,@"UID":@501,@"Start":@"started",@"BundleID":@"owned.fixture",@"OSBuild":@"test-os"};
}
static NSDictionary *ACExecutableIdentity(pid_t pid, NSString *bundle) { return metadata; }
#include "pid-process-source.inc"
int main(int argc, const char **argv) {
  @autoreleasepool {
    if(argc!=2)return 10;
    NSData *raw=[NSData dataWithContentsOfFile:@(argv[1])];
    NSDictionary *go=[NSJSONSerialization JSONObjectWithData:raw options:0 error:nil];
    NSDictionary *partial=@{@"ExecutablePath":@"/owned/fixture",@"SigningID":@"selected-helper",@"CodeHash":@"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"};
    NSMutableDictionary *full=[partial mutableCopy];full[@"AppVersion"]=@"1";full[@"AppBuild"]=@"2";
    NSDictionary *variants=@{@"partial":partial,@"full":full,@"empty":@{}};
    for(NSString *name in @[@"partial",@"full",@"empty"]){
      metadata=variants[name];NSDictionary *native=ACPIDProcess(123);
      if(![native isEqual:go[name]]){fprintf(stderr,"identity JSON mismatch: %s\n",name.UTF8String);return 20;}
      if(ACProcess(123)[@"AppVersion"]||ACProcess(123)[@"ExecutablePath"]){fprintf(stderr,"base identity was expanded\n");return 21;}
    }
    puts("partial/full/empty native metadata equal Go Process JSON; base identity unchanged");
    return 0;
  }
}
