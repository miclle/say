#ifndef SAY_NATIVE_DESKTOP_H
#define SAY_NATIVE_DESKTOP_H

#include <stdint.h>

typedef void *SayDesktopHandle;

enum {
    SayDesktopCommandToggle = 1,
    SayDesktopCommandBackward = 2,
    SayDesktopCommandForward = 3,
    SayDesktopCommandResume = 4,
    SayDesktopCommandPause = 5,
};

SayDesktopHandle say_desktop_create(uintptr_t token);
void say_desktop_render(SayDesktopHandle handle,
                        const char *document,
                        const char *text,
                        const char *display_text,
                        int playing,
                        int busy,
                        int queue_index,
                        int queue_count,
                        double position,
                        double duration);
void say_desktop_clear(SayDesktopHandle handle);
void say_desktop_run(void);
void say_desktop_stop(void);
void say_desktop_destroy(SayDesktopHandle handle);
int say_desktop_now_playing_is_clear(void);
int say_desktop_status_items_visible(SayDesktopHandle handle);
int say_desktop_remote_commands_registered(SayDesktopHandle handle);
int say_desktop_status_title_equals(SayDesktopHandle handle, const char *text);
int say_desktop_now_playing_title_equals(const char *text);

#endif
