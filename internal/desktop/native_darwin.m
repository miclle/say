#import "native_darwin.h"

#import <AppKit/AppKit.h>
#import <MediaPlayer/MediaPlayer.h>

extern void sayDesktopEmit(uintptr_t token, int command);

@interface SayDesktopController : NSObject
@property(nonatomic) uintptr_t token;
@property(nonatomic, strong) NSStatusItem *titleItem;
@property(nonatomic, strong) NSStatusItem *backwardItem;
@property(nonatomic, strong) NSStatusItem *toggleItem;
@property(nonatomic, strong) NSStatusItem *forwardItem;
@property(nonatomic, strong) id playTarget;
@property(nonatomic, strong) id pauseTarget;
@property(nonatomic, strong) id toggleTarget;
@property(nonatomic, strong) id previousTarget;
@property(nonatomic, strong) id nextTarget;
@property(nonatomic) BOOL remoteCommandsRegistered;
@end

@implementation SayDesktopController

- (instancetype)initWithToken:(uintptr_t)token {
    self = [super init];
    if (self == nil) {
        return nil;
    }
    _token = token;

    NSStatusBar *statusBar = [NSStatusBar systemStatusBar];
    // New status items are inserted from right to left. Create the controls in
    // reverse order so the visible group reads title, previous, toggle, next.
    _forwardItem = [statusBar statusItemWithLength:NSSquareStatusItemLength];
    _toggleItem = [statusBar statusItemWithLength:NSSquareStatusItemLength];
    _backwardItem = [statusBar statusItemWithLength:NSSquareStatusItemLength];
    _titleItem = [statusBar statusItemWithLength:220.0];

    [self configureButton:_backwardItem.button
                   symbol:@"backward.end.fill"
                    label:@"Previous sentence"
                   action:@selector(previous:)];
    [self configureButton:_toggleItem.button
                   symbol:@"pause.fill"
                    label:@"Pause"
                   action:@selector(toggle:)];
    [self configureButton:_forwardItem.button
                   symbol:@"forward.end.fill"
                    label:@"Next sentence"
                   action:@selector(next:)];

    _titleItem.button.title = @"";
    _titleItem.button.enabled = NO;
    _titleItem.button.cell.lineBreakMode = NSLineBreakByTruncatingTail;
    [self setVisible:NO];
    return self;
}

- (void)configureButton:(NSStatusBarButton *)button
                  symbol:(NSString *)symbol
                   label:(NSString *)label
                  action:(SEL)action {
    NSImage *image = [NSImage imageWithSystemSymbolName:symbol accessibilityDescription:label];
    image.template = YES;
    button.image = image;
    button.imagePosition = NSImageOnly;
    button.target = self;
    button.action = action;
    button.toolTip = label;
    button.accessibilityLabel = label;
}

- (void)setVisible:(BOOL)visible {
    self.titleItem.visible = visible;
    self.backwardItem.visible = visible;
    self.toggleItem.visible = visible;
    self.forwardItem.visible = visible;
}

- (void)previous:(id)sender {
    sayDesktopEmit(self.token, SayDesktopCommandBackward);
}

- (void)toggle:(id)sender {
    sayDesktopEmit(self.token, SayDesktopCommandToggle);
}

- (void)next:(id)sender {
    sayDesktopEmit(self.token, SayDesktopCommandForward);
}

- (id)addHandler:(MPRemoteCommand *)command value:(int)value {
    __weak SayDesktopController *weakSelf = self;
    command.enabled = YES;
    return [command addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
        SayDesktopController *strongSelf = weakSelf;
        if (strongSelf == nil) {
            return MPRemoteCommandHandlerStatusNoActionableNowPlayingItem;
        }
        sayDesktopEmit(strongSelf.token, value);
        return MPRemoteCommandHandlerStatusSuccess;
    }];
}

- (void)registerRemoteCommands {
    if (self.remoteCommandsRegistered) {
        return;
    }
    MPRemoteCommandCenter *commands = [MPRemoteCommandCenter sharedCommandCenter];
    self.playTarget = [self addHandler:commands.playCommand value:SayDesktopCommandResume];
    self.pauseTarget = [self addHandler:commands.pauseCommand value:SayDesktopCommandPause];
    self.toggleTarget = [self addHandler:commands.togglePlayPauseCommand value:SayDesktopCommandToggle];
    self.previousTarget = [self addHandler:commands.previousTrackCommand value:SayDesktopCommandBackward];
    self.nextTarget = [self addHandler:commands.nextTrackCommand value:SayDesktopCommandForward];
    self.remoteCommandsRegistered = YES;
}

- (void)removeRemoteCommands {
    if (!self.remoteCommandsRegistered) {
        return;
    }
    MPRemoteCommandCenter *commands = [MPRemoteCommandCenter sharedCommandCenter];
    [commands.playCommand removeTarget:self.playTarget];
    [commands.pauseCommand removeTarget:self.pauseTarget];
    [commands.togglePlayPauseCommand removeTarget:self.toggleTarget];
    [commands.previousTrackCommand removeTarget:self.previousTarget];
    [commands.nextTrackCommand removeTarget:self.nextTarget];
    self.playTarget = nil;
    self.pauseTarget = nil;
    self.toggleTarget = nil;
    self.previousTarget = nil;
    self.nextTarget = nil;
    self.remoteCommandsRegistered = NO;
}

- (void)renderDocument:(NSString *)document
                   text:(NSString *)text
            displayText:(NSString *)displayText
                playing:(BOOL)playing
                   busy:(BOOL)busy
             queueIndex:(NSInteger)queueIndex
             queueCount:(NSInteger)queueCount
               position:(NSTimeInterval)position
               duration:(NSTimeInterval)duration {
    [self registerRemoteCommands];
    self.titleItem.button.title = busy ? [@"… " stringByAppendingString:displayText] : displayText;
    self.titleItem.button.toolTip = document;
    NSString *toggleSymbol = playing ? @"pause.fill" : @"play.fill";
    NSString *toggleLabel = playing ? @"Pause" : @"Play";
    NSImage *toggleImage = [NSImage imageWithSystemSymbolName:toggleSymbol accessibilityDescription:toggleLabel];
    toggleImage.template = YES;
    self.toggleItem.button.image = toggleImage;
    self.toggleItem.button.toolTip = toggleLabel;
    self.toggleItem.button.accessibilityLabel = toggleLabel;
    [self setVisible:YES];

    BOOL advancing = playing && !busy;
    NSMutableDictionary<NSString *, id> *info = [@{
        MPMediaItemPropertyTitle : text,
        MPMediaItemPropertyAlbumTitle : document,
        MPNowPlayingInfoPropertyPlaybackQueueIndex : @(queueIndex),
        MPNowPlayingInfoPropertyPlaybackQueueCount : @(queueCount),
        MPNowPlayingInfoPropertyPlaybackRate : @(advancing ? 1.0 : 0.0),
        MPNowPlayingInfoPropertyMediaType : @(MPNowPlayingInfoMediaTypeAudio),
    } mutableCopy];
    if (duration > 0) {
        info[MPMediaItemPropertyPlaybackDuration] = @(duration);
        info[MPNowPlayingInfoPropertyElapsedPlaybackTime] = @(MAX(0, position));
    }
    MPNowPlayingInfoCenter *center = [MPNowPlayingInfoCenter defaultCenter];
    center.nowPlayingInfo = info;
    if (busy && playing) {
        center.playbackState = MPNowPlayingPlaybackStateInterrupted;
    } else {
        center.playbackState = playing ? MPNowPlayingPlaybackStatePlaying : MPNowPlayingPlaybackStatePaused;
    }
}

- (void)clear {
    [self setVisible:NO];
    [self removeRemoteCommands];
    MPNowPlayingInfoCenter *center = [MPNowPlayingInfoCenter defaultCenter];
    center.nowPlayingInfo = nil;
    center.playbackState = MPNowPlayingPlaybackStateStopped;
}

- (void)destroy {
    [self clear];
    NSStatusBar *statusBar = [NSStatusBar systemStatusBar];
    [statusBar removeStatusItem:self.titleItem];
    [statusBar removeStatusItem:self.backwardItem];
    [statusBar removeStatusItem:self.toggleItem];
    [statusBar removeStatusItem:self.forwardItem];
}

@end

SayDesktopHandle say_desktop_create(uintptr_t token) {
    @autoreleasepool {
        NSApplication *application = [NSApplication sharedApplication];
        [application setActivationPolicy:NSApplicationActivationPolicyAccessory];
        SayDesktopController *controller = [[SayDesktopController alloc] initWithToken:token];
        return (__bridge_retained void *)controller;
    }
}

void say_desktop_render(SayDesktopHandle handle,
                        const char *document,
                        const char *text,
                        const char *display_text,
                        int playing,
                        int busy,
                        int queue_index,
                        int queue_count,
                        double position,
                        double duration) {
    if (handle == NULL) {
        return;
    }
    NSString *documentCopy = document == NULL ? @"say" : [NSString stringWithUTF8String:document];
    NSString *textCopy = text == NULL ? @"" : [NSString stringWithUTF8String:text];
    NSString *displayTextCopy = display_text == NULL ? @"" : [NSString stringWithUTF8String:display_text];
    SayDesktopController *controller = (__bridge SayDesktopController *)handle;
    dispatch_async(dispatch_get_main_queue(), ^{
        [controller renderDocument:documentCopy
                              text:textCopy
                       displayText:displayTextCopy
                           playing:playing != 0
                              busy:busy != 0
                        queueIndex:queue_index
                        queueCount:queue_count
                          position:position
                          duration:duration];
    });
}

void say_desktop_clear(SayDesktopHandle handle) {
    if (handle == NULL) {
        return;
    }
    SayDesktopController *controller = (__bridge SayDesktopController *)handle;
    dispatch_async(dispatch_get_main_queue(), ^{
        [controller clear];
    });
}

void say_desktop_run(void) {
    @autoreleasepool {
        [NSApp run];
    }
}

void say_desktop_stop(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp stop:nil];
        NSEvent *event = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
                                            location:NSZeroPoint
                                       modifierFlags:0
                                           timestamp:0
                                        windowNumber:0
                                             context:nil
                                             subtype:0
                                               data1:0
                                               data2:0];
        [NSApp postEvent:event atStart:NO];
    });
}

void say_desktop_destroy(SayDesktopHandle handle) {
    if (handle == NULL) {
        return;
    }
    SayDesktopController *controller = CFBridgingRelease(handle);
    [controller destroy];
}

static void say_desktop_sync_main(dispatch_block_t block) {
    if ([NSThread isMainThread]) {
        block();
    } else {
        dispatch_sync(dispatch_get_main_queue(), block);
    }
}

int say_desktop_now_playing_is_clear(void) {
    __block BOOL clear = NO;
    say_desktop_sync_main(^{
        MPNowPlayingInfoCenter *center = [MPNowPlayingInfoCenter defaultCenter];
        clear = center.nowPlayingInfo == nil && center.playbackState == MPNowPlayingPlaybackStateStopped;
    });
    return clear;
}

int say_desktop_status_items_visible(SayDesktopHandle handle) {
    if (handle == NULL) {
        return 0;
    }
    SayDesktopController *controller = (__bridge SayDesktopController *)handle;
    __block BOOL visible = NO;
    say_desktop_sync_main(^{
        visible = controller.titleItem.visible && controller.backwardItem.visible &&
                  controller.toggleItem.visible && controller.forwardItem.visible;
    });
    return visible;
}

int say_desktop_remote_commands_registered(SayDesktopHandle handle) {
    if (handle == NULL) {
        return 0;
    }
    SayDesktopController *controller = (__bridge SayDesktopController *)handle;
    __block BOOL registered = NO;
    say_desktop_sync_main(^{
        registered = controller.remoteCommandsRegistered;
    });
    return registered;
}

int say_desktop_status_title_equals(SayDesktopHandle handle, const char *text) {
    if (handle == NULL || text == NULL) {
        return 0;
    }
    SayDesktopController *controller = (__bridge SayDesktopController *)handle;
    NSString *expected = [NSString stringWithUTF8String:text];
    __block BOOL equal = NO;
    say_desktop_sync_main(^{
        equal = [controller.titleItem.button.title isEqualToString:expected];
    });
    return equal;
}

int say_desktop_now_playing_title_equals(const char *text) {
    if (text == NULL) {
        return 0;
    }
    NSString *expected = [NSString stringWithUTF8String:text];
    __block BOOL equal = NO;
    say_desktop_sync_main(^{
        NSString *title = [MPNowPlayingInfoCenter defaultCenter].nowPlayingInfo[MPMediaItemPropertyTitle];
        equal = [title isEqualToString:expected];
    });
    return equal;
}
