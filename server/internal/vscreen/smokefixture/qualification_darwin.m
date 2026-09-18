//go:build darwin && cgo

#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <Carbon/Carbon.h>
#import <Security/Security.h>
#import "bridge.h"
#include <fcntl.h>
#include <unistd.h>

char *multica_input_qualification_source(void) {
  TISInputSourceRef source=TISCopyCurrentKeyboardInputSource();
  NSString *value=source?(__bridge NSString *)TISGetInputSourceProperty(source,kTISPropertyInputSourceID):nil;
  char *out=value?strdup(value.UTF8String):NULL;if(source)CFRelease(source);return out;
}
char *multica_input_qualification_code(const char *path) {
 @autoreleasepool {
  SecStaticCodeRef code=NULL;CFDictionaryRef info=NULL;
  if(SecStaticCodeCreateWithPath((__bridge CFURLRef)[NSURL fileURLWithPath:@(path)],kSecCSDefaultFlags,&code)!=errSecSuccess)return NULL;
  OSStatus status=SecStaticCodeCheckValidity(code,kSecCSDefaultFlags,NULL);
  if(status==errSecSuccess)status=SecCodeCopySigningInformation(code,kSecCSDefaultFlags,&info);
  CFRelease(code);if(status!=errSecSuccess||!info){if(info)CFRelease(info);return NULL;}
  NSDictionary *details=CFBridgingRelease(info);NSData *hash=details[(__bridge id)kSecCodeInfoUnique];
  if(![hash isKindOfClass:NSData.class]||(hash.length!=20&&hash.length!=32))return NULL;
  NSMutableString *out=[NSMutableString string];const unsigned char *bytes=hash.bytes;for(NSUInteger i=0;i<hash.length;i++)[out appendFormat:@"%02x",bytes[i]];return strdup(out.UTF8String);
 }
}
static BOOL qualificationWrite(NSString *directory, NSData *data) {
 NSString *temporary=[directory stringByAppendingPathComponent:@"readback.pending"];
 int fd=open(temporary.fileSystemRepresentation,O_WRONLY|O_CREAT|O_EXCL|O_NOFOLLOW,0600);if(fd<0)return NO;
 const char *bytes=data.bytes;NSUInteger written=0;while(written<data.length){ssize_t n=write(fd,bytes+written,data.length-written);if(n<=0)break;written+=(NSUInteger)n;}
 BOOL okay=close(fd)==0&&written==data.length;
 NSString *target=[directory stringByAppendingPathComponent:@"readback.json"];
 if(okay)okay=rename(temporary.fileSystemRepresentation,target.fileSystemRepresentation)==0;
 if(!okay)unlink(temporary.fileSystemRepresentation);return okay;
}
static NSString *qStart(int pid) {char *p=multica_smoke_fixture_start(pid);if(!p)return nil;NSString *s=@(p);free(p);return s;}

// A local monitor runs before dispatch and cannot prove target processing.
// Only a final release/scroll event may publish a completion token, after the
// real responder has handled it and a subsequent main-queue turn has rendered.
@interface MVQApplication : NSApplication
@property unsigned long long processedToken;
@property NSUInteger processedType;
@end
@implementation MVQApplication
- (void)sendEvent:(NSEvent *)event {
 CGEventRef cg=event.CGEvent;
 unsigned long long token=cg?(unsigned long long)CGEventGetIntegerValueField(cg,kCGEventSourceUserData):0;
 BOOL final=event.type==NSEventTypeKeyUp||event.type==NSEventTypeLeftMouseUp||event.type==NSEventTypeScrollWheel;
 [super sendEvent:event];
 if(token&&final){NSUInteger type=event.type;dispatch_async(dispatch_get_main_queue(),^{for(NSWindow *window in self.windows)[window displayIfNeeded];self.processedType=type;self.processedToken=token;});}
}
@end

@interface MVQEditor : NSView<NSTextInputClient>
@property NSMutableString *text;
@property NSRange selection;
@property BOOL manual, manualStarted, manualConfirmed, manualFinished;
@property unsigned long long keyCount;
@property long long firstKeyNS, lastKeyNS;
@end
@implementation MVQEditor
- (BOOL)acceptsFirstResponder {return YES;}
- (BOOL)isAccessibilityElement {return YES;}
- (NSString *)accessibilityRole {return NSAccessibilityTextFieldRole;}
- (NSString *)accessibilityTitle {return @"Qualification Unicode input";}
- (id)accessibilityValue {return self.text;}
- (BOOL)isAccessibilityFocused {return self.window.firstResponder==self;}
- (BOOL)isAccessibilitySelectorAllowed:(SEL)selector {if(selector==@selector(setAccessibilityValue:))return NO;return [super isAccessibilitySelectorAllowed:selector];}
- (void)drawRect:(NSRect)dirty {[[NSColor whiteColor]setFill];NSRectFill(self.bounds);[self.text drawAtPoint:NSMakePoint(8,12) withAttributes:@{NSFontAttributeName:[NSFont systemFontOfSize:20],NSForegroundColorAttributeName:NSColor.blackColor}];}
- (void)keyDown:(NSEvent *)event {
 if(self.manual){if(!self.manualStarted)return;self.keyCount++;long long now=(long long)(NSDate.date.timeIntervalSince1970*1e9);if(!self.firstKeyNS)self.firstKeyNS=now;self.lastKeyNS=now;}
 [self interpretKeyEvents:@[event]];
}
- (void)insertText:(id)value replacementRange:(NSRange)range {
 NSString *text=[value isKindOfClass:NSAttributedString.class]?[value string]:value;if(![text isKindOfClass:NSString.class])return;
 if(range.location==NSNotFound)range=self.selection;if(NSMaxRange(range)>self.text.length||self.text.length-range.length+text.length>128)return;
 [self.text replaceCharactersInRange:range withString:text];self.selection=NSMakeRange(range.location+text.length,0);[self setNeedsDisplay:YES];
}
- (void)doCommandBySelector:(SEL)selector {
 if(selector==@selector(moveLeft:)){if(self.selection.location>0)self.selection=NSMakeRange(self.selection.location-1,0);}
 else if(selector==@selector(deleteBackward:)){if(self.selection.length){[self.text deleteCharactersInRange:self.selection];self.selection=NSMakeRange(self.selection.location,0);}else if(self.selection.location>0){NSRange range=[self.text rangeOfComposedCharacterSequenceAtIndex:self.selection.location-1];[self.text deleteCharactersInRange:range];self.selection=NSMakeRange(range.location,0);}}
 [self setNeedsDisplay:YES];
}
- (void)setMarkedText:(id)value selectedRange:(NSRange)range replacementRange:(NSRange)replacement {}
- (void)unmarkText {}
- (BOOL)hasMarkedText {return NO;}
- (NSRange)markedRange {return NSMakeRange(NSNotFound,0);}
- (NSRange)selectedRange {return self.selection;}
- (NSArray *)validAttributesForMarkedText {return @[];}
- (NSAttributedString *)attributedSubstringForProposedRange:(NSRange)range actualRange:(NSRangePointer)actual {if(NSMaxRange(range)>self.text.length)return nil;if(actual)*actual=range;return [[NSAttributedString alloc]initWithString:[self.text substringWithRange:range]];}
- (NSUInteger)characterIndexForPoint:(NSPoint)point {return self.selection.location;}
- (NSRect)firstRectForCharacterRange:(NSRange)range actualRange:(NSRangePointer)actual {if(actual)*actual=range;return [self.window convertRectToScreen:[self convertRect:self.bounds toView:nil]];}
@end

@interface MVQManualControls : NSObject
@property MVQEditor *editor;
- (void)begin:(id)sender;
- (void)confirm:(id)sender;
@end
@implementation MVQManualControls
- (void)begin:(id)sender {if(self.editor.manualStarted)return;self.editor.manualStarted=YES;[self.editor.window makeFirstResponder:self.editor];}
- (void)confirm:(id)sender {
 if(self.editor.manualStarted&&self.editor.manualFinished&&self.editor.keyCount==6&&[self.editor.text isEqual:@"ABCDEZ"]&&NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier==getpid())self.editor.manualConfirmed=YES;
}
@end

@interface MVQToggle : NSView
@property BOOL on;
@end
@implementation MVQToggle
- (BOOL)acceptsFirstMouse:(NSEvent *)event {return YES;}
- (BOOL)isAccessibilityElement {return YES;}
- (NSString *)accessibilityRole {return NSAccessibilityGroupRole;}
- (NSString *)accessibilityTitle {return @"Qualification PID toggle";}
- (id)accessibilityValue {return self.on?@"on":@"off";}
- (void)mouseUp:(NSEvent *)event {if(NSPointInRect([self convertPoint:event.locationInWindow fromView:nil],self.bounds)){self.on=!self.on;[self setNeedsDisplay:YES];}}
- (void)drawRect:(NSRect)dirty {[(self.on?NSColor.systemGreenColor:NSColor.systemRedColor)setFill];NSRectFill(self.bounds);[(self.on?@"ON":@"OFF")drawAtPoint:NSMakePoint(8,12)withAttributes:@{NSFontAttributeName:[NSFont systemFontOfSize:20]}];}
@end
@interface MVQScroll : NSScrollView
@end
@implementation MVQScroll
- (NSString *)accessibilityTitle {return @"Qualification scroll";}
@end
@interface MVQCanvas : NSView
@property NSRect object;
@property BOOL dragging;
@property NSPoint anchor;
@end
@implementation MVQCanvas
- (BOOL)acceptsFirstMouse:(NSEvent *)event {return YES;}
- (BOOL)isAccessibilityElement {return YES;}
- (NSString *)accessibilityRole {return NSAccessibilityGroupRole;}
- (NSString *)accessibilityTitle {return @"Qualification draggable object";}
- (void)drawRect:(NSRect)dirty {[[NSColor lightGrayColor]setFill];NSRectFill(self.bounds);[[NSColor systemBlueColor]setFill];NSRectFill(self.object);}
- (void)mouseDown:(NSEvent *)event {NSPoint p=[self convertPoint:event.locationInWindow fromView:nil];self.dragging=NSPointInRect(p,self.object);self.anchor=NSMakePoint(p.x-self.object.origin.x,p.y-self.object.origin.y);}
- (void)mouseDragged:(NSEvent *)event {if(!self.dragging)return;NSPoint p=[self convertPoint:event.locationInWindow fromView:nil];self.object=NSMakeRect(MAX(0,MIN(self.bounds.size.width-40,p.x-self.anchor.x)),MAX(0,MIN(self.bounds.size.height-40,p.y-self.anchor.y)),40,40);[self setNeedsDisplay:YES];}
- (void)mouseUp:(NSEvent *)event {self.dragging=NO;}
@end

int multica_input_qualification_run(const char *raw) {
 @autoreleasepool {
  if(!getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE")||strcmp(getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE"),"1"))return 1;
  NSDictionary *cfg=[NSJSONSerialization JSONObjectWithData:[@(raw)dataUsingEncoding:NSUTF8StringEncoding]options:0 error:nil];
  if(!cfg||![NSBundle.mainBundle.bundleIdentifier isEqual:cfg[@"BundleID"]]||![qStart([cfg[@"OwnerPID"]intValue])isEqual:cfg[@"OwnerStart"]])return 1;
  [MVQApplication sharedApplication];[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
  NSWindow *window=[[NSWindow alloc]initWithContentRect:NSMakeRect(100,100,640,480)styleMask:NSWindowStyleMaskTitled|NSWindowStyleMaskClosable backing:NSBackingStoreBuffered defer:NO];window.releasedWhenClosed=NO;window.title=[cfg[@"InteractiveScratch"]boolValue]?@"Multica manual typing qualification":@"Multica input qualification (test-owned)";
  MVQEditor *editor=[[MVQEditor alloc]initWithFrame:NSMakeRect(20,360,600,60)];editor.text=[NSMutableString string];editor.selection=NSMakeRange(0,0);editor.manual=[cfg[@"InteractiveScratch"]boolValue];
  MVQToggle *toggle=[[MVQToggle alloc]initWithFrame:NSMakeRect(20,285,140,50)];
  MVQCanvas *canvas=[[MVQCanvas alloc]initWithFrame:NSMakeRect(20,20,600,110)];canvas.object=NSMakeRect(20,30,40,40);
  MVQScroll *scroll=[[MVQScroll alloc]initWithFrame:NSMakeRect(20,150,600,110)];scroll.hasVerticalScroller=YES;scroll.verticalScrollElasticity=NSScrollElasticityNone;
  NSTextView *document=[[NSTextView alloc]initWithFrame:NSMakeRect(0,0,570,1600)];document.editable=NO;document.selectable=NO;NSMutableString *lines=[NSMutableString string];for(int i=0;i<60;i++)[lines appendFormat:@"Visible scroll row %02d\n",i];document.string=lines;scroll.documentView=document;[scroll.contentView scrollToPoint:NSZeroPoint];[scroll reflectScrolledClipView:scroll.contentView];
  MVQManualControls *manual=[MVQManualControls new];manual.editor=editor;
  NSTextField *prompt=[NSTextField wrappingLabelWithString:@"Click Start. Type each requested letter yourself while background actions run."];prompt.frame=NSMakeRect(20,280,600,65);
  NSButton *start=[[NSButton alloc]initWithFrame:NSMakeRect(20,200,180,50)];start.title=@"Start typing challenge";start.target=manual;start.action=@selector(begin:);
  NSButton *confirm=[[NSButton alloc]initWithFrame:NSMakeRect(230,200,300,50)];confirm.title=@"Confirm I typed the challenge";confirm.target=manual;confirm.action=@selector(confirm:);confirm.enabled=NO;
  NSArray *views=editor.manual?@[editor,prompt,start,confirm]:@[editor,toggle,canvas,scroll];
  for(NSView *view in views)[window.contentView addSubview:view];[window makeFirstResponder:editor];if(editor.manual)[window orderFront:nil];else [window orderBack:nil];
  __block BOOL closed=NO,failed=NO;NSString *directory=cfg[@"Directory"],*nonce=cfg[@"Nonce"];
  void (^publish)(void)=^{
   CGRect bounds=CGRectZero;NSArray *windows=CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionIncludingWindow,(CGWindowID)window.windowNumber));for(NSDictionary *item in windows){if([item[(id)kCGWindowOwnerPID]intValue]==getpid())CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)item[(id)kCGWindowBounds],&bounds);}
   AXUIElementRef app=AXUIElementCreateApplication(getpid());CFTypeRef focused=NULL;BOOL actual=AXUIElementCopyAttributeValue(app,kAXFocusedUIElementAttribute,&focused)==kAXErrorSuccess&&focused!=NULL;if(focused)CFRelease(focused);CFRelease(app);
   char *source=multica_input_qualification_source();NSString *sourceID=source?@(source):@"";if(source)free(source);
   NSDictionary *state=@{@"manual_started":@(editor.manualStarted),@"manual_confirmed":@(editor.manualConfirmed),@"key_count":@(editor.keyCount),@"first_key_ns":@(editor.firstKeyNS),@"last_key_ns":@(editor.lastKeyNS),@"last_processed_token":@(((MVQApplication *)NSApp).processedToken),@"last_event_type":@(((MVQApplication *)NSApp).processedType),@"nonce":nonce,@"pid":@(getpid()),@"process_start":qStart(getpid())?:@"",@"window_id":@(closed?0:window.windowNumber),@"display_id":closed?@0:window.screen.deviceDescription[@"NSScreenNumber"]?:@0,@"bounds":@{@"x":@(bounds.origin.x),@"y":@(bounds.origin.y),@"width":@(bounds.size.width),@"height":@(bounds.size.height)},@"closed":@(closed),@"text":editor.text,@"toggle":@(toggle.on),@"SelectionLocation":@(editor.selection.location),@"SelectionLength":@(editor.selection.length),@"ScrollOffset":@(scroll.contentView.bounds.origin.y),@"ObjectX":@(canvas.object.origin.x),@"ObjectY":@(canvas.object.origin.y),@"ax_focused":@(actual),@"input_source":sourceID};
   NSData *json=[NSJSONSerialization dataWithJSONObject:state options:0 error:nil];
   if(!json||!qualificationWrite(directory,json))failed=YES;
  };
  [window makeFirstResponder:editor];publish();NSDate *deadline=[NSDate dateWithTimeIntervalSinceNow:90];
  NSTimer *watchdog=[NSTimer scheduledTimerWithTimeInterval:0.02 repeats:YES block:^(NSTimer *timer){
   if(manual.editor.manual){
    NSString *path=[directory stringByAppendingPathComponent:@"manual-progress"];NSDictionary *attrs=[NSFileManager.defaultManager attributesOfItemAtPath:path error:nil];
    if([attrs[NSFileSize]unsignedLongLongValue]<4096){NSData *data=[NSData dataWithContentsOfFile:path];NSDictionary *progress=data?[NSJSONSerialization JSONObjectWithData:data options:0 error:nil]:nil;NSInteger stage=[progress[@"Stage"]integerValue];
     if([progress[@"Nonce"]isEqual:nonce]&&stage>=1&&stage<=6){NSString *letter=[@"ABCDEZ" substringWithRange:NSMakeRange(stage-1,1)];prompt.stringValue=stage==6?@"Background actions completed. Type Z, then click Confirm.":[NSString stringWithFormat:@"%@Type %@ yourself. Wait for the next prompt.",editor.manualStarted?@"":@"Click Start, then ",letter];editor.manualFinished=stage==6;confirm.enabled=stage==6;}
    }
   }
   NSString *stop=[NSString stringWithContentsOfFile:[directory stringByAppendingPathComponent:@"stop"]encoding:NSUTF8StringEncoding error:nil];
   if(failed||deadline.timeIntervalSinceNow<=0||[stop isEqual:nonce]||![qStart([cfg[@"OwnerPID"]intValue])isEqual:cfg[@"OwnerStart"]]){[timer invalidate];[window close];closed=YES;publish();[NSApp stop:nil];[NSApp postEvent:[NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0]atStart:NO];}else publish();
  }];
  [NSApp run];[watchdog invalidate];if(!closed){[window close];closed=YES;publish();}return failed?1:0;
 }
}
