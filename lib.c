#include <stdio.h>

/* Everything else that used to live here -- gettimeofday, sleep, usleep,
 * vTaskDelay, esp_fill_random, esp_random, strrchr, esp_wifi_connect and
 * esp_wifi_disconnect -- is now implemented in Go, in lib.go.
 *
 * esp_timer_get_time moved too, in the sense that it simply went away: the copy
 * here was a duplicate of the weak definition in esp_timer_shim.c, which is what
 * links now that nothing defines the symbol strongly.
 *
 * __assert_func stays in C because it needs printf, and C varargs have no Go
 * equivalent. */

void __assert_func(const char *file, int line, const char *func, const char *expr) {
    printf("ASSERT FAILED: %s:%d %s: %s\n", file, line, func ? func : "", expr ? expr : "");
    while (1) {}
}
