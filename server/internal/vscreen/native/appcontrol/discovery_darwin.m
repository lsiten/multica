// go:build darwin && cgo

#import "native_internal.h"

NSDictionary *ACListApps(ACRequest *r, NSString **error) {
  NSMutableDictionary<NSString *, NSDictionary *> *apps = [NSMutableDictionary new];
  NSArray<NSURL *> *roots = @[
    [NSURL fileURLWithPath:@"/Applications" isDirectory:YES],
    [NSURL fileURLWithPath:@"/System/Applications" isDirectory:YES],
    [NSURL fileURLWithPath:[NSHomeDirectory() stringByAppendingPathComponent:@"Applications"] isDirectory:YES]
  ];
  NSUInteger visited = 0, responseBytes = 0;
  __block BOOL truncated = NO;
  for (NSURL *root in roots) {
    NSDirectoryEnumerator *enumerator = [NSFileManager.defaultManager
      enumeratorAtURL:root includingPropertiesForKeys:nil
      options:NSDirectoryEnumerationSkipsHiddenFiles | NSDirectoryEnumerationSkipsPackageDescendants
      errorHandler:^BOOL(NSURL *url, NSError *failure) { truncated = YES; return YES; }];
    for (NSURL *url in enumerator) {
      if (ACExpired(r) || ++visited > 8192 || apps.count >= 512) {
        truncated = YES;
        break;
      }
      if (![url.pathExtension.lowercaseString isEqual:@"app"])
        continue;
      NSString *bundle = [NSBundle bundleWithURL:url].bundleIdentifier;
      if (!bundle.length || [bundle lengthOfBytesUsingEncoding:NSUTF8StringEncoding] > 255 || apps[bundle])
        continue;
      NSURL *resolved = [NSWorkspace.sharedWorkspace URLForApplicationWithBundleIdentifier:bundle];
      NSBundle *installed = resolved ? [NSBundle bundleWithURL:resolved] : nil;
      if (![installed.bundleIdentifier isEqual:bundle])
        continue;
      NSString *name = [NSFileManager.defaultManager displayNameAtPath:resolved.path];
      if (!name.length || [name lengthOfBytesUsingEncoding:NSUTF8StringEncoding] > 1024)
        continue;
      NSDictionary *entry = @{ @"bundle_id": bundle, @"name": name,
        @"running": @([NSRunningApplication runningApplicationsWithBundleIdentifier:bundle].count > 0) };
      responseBytes += [NSJSONSerialization dataWithJSONObject:entry options:0 error:nil].length + 1;
      if (responseBytes > 47 * 1024) { truncated = YES; break; }
      apps[bundle] = entry;
    }
    if (ACExpired(r) || visited > 8192 || apps.count >= 512 || responseBytes > 47 * 1024)
      break;
  }
  NSMutableArray *result = [NSMutableArray new];
  for (NSString *bundle in [apps.allKeys sortedArrayUsingSelector:@selector(compare:)])
    [result addObject:apps[bundle]];
  return @{ @"apps": result, @"truncated": @(truncated) };
}
