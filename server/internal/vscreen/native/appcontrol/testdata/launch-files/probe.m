#import <Foundation/Foundation.h>
static NSArray<NSURL *> *ACRequestedFiles(NSDictionary *input, NSString **error);
#include "launch-files.inc"
static int check(NSDictionary *input, NSString *expectedError, NSString *expectedPath) {
  NSString *error = nil;
  NSArray<NSURL *> *files = ACRequestedFiles(input, &error);
  if (expectedError) return files == nil && [error isEqual:expectedError];
  if (files == nil || files.count != (expectedPath ? 1 : 0)) return 0;
  return !expectedPath || [files[0].path isEqual:expectedPath];
}
int main(int argc, const char **argv) { @autoreleasepool {
  NSString *path = [NSString stringWithUTF8String:argv[1]];
  NSString *directory = [NSString stringWithUTF8String:argv[2]];
  NSDictionary *cases[] = {
    @{}, @{@"Files": NSNull.null}, @{@"Files": @[]},
    @{@"Files": @"bad"}, @{@"Files": @[@1]},
    @{@"Files": @[path]}, @{@"Files": @[@"relative"]},
    @{@"Files": @[@"/definitely/missing"]}, @{@"Files": @[directory]},
  };
  BOOL ok = check(cases[0],nil,nil) && check(cases[1],nil,nil) && check(cases[2],nil,nil) &&
    check(cases[3],@"invalid_launch",nil) && check(cases[4],@"invalid_launch",nil) &&
    check(cases[5],nil,path) && check(cases[6],@"invalid_launch",nil) &&
    check(cases[7],@"invalid_launch",nil) && check(cases[8],@"invalid_launch",nil);
  return ok ? 0 : 1;
}}
