#include <stdint.h>
#include <stddef.h>
#include <string.h>
#include <stdio.h>

#include "include.h"

extern uint64_t espradio_time_us_now(void);
extern void espradio_task_delay(uint32_t ticks);

int gettimeofday(void *tv, void *tz) {
    (void)tz;
    if (tv) {
        uint64_t us = espradio_time_us_now();
        struct { uint32_t sec; uint32_t usec; } *t = tv;
        t->sec  = (uint32_t)(us / 1000000u);
        t->usec = (uint32_t)(us % 1000000u);
    }
    return 0;
}

static uint32_t xorshift_state = 0x12345678u;

void esp_fill_random(void *buf, size_t len) {
    uint8_t *p = (uint8_t *)buf;
    while (len >= 4) {
        uint32_t t = (uint32_t)espradio_time_us_now();
        xorshift_state ^= t + 0x85ebca6bu + (xorshift_state << 6) + (xorshift_state >> 2);
        xorshift_state ^= xorshift_state << 13;
        xorshift_state ^= xorshift_state >> 17;
        xorshift_state ^= xorshift_state << 5;
        memcpy(p, &xorshift_state, 4);
        p += 4; len -= 4;
    }
    if (len) {
        uint32_t t = (uint32_t)espradio_time_us_now();
        xorshift_state ^= t + 0x85ebca6bu + (xorshift_state << 6) + (xorshift_state >> 2);
        memcpy(p, &xorshift_state, len);
    }
}

unsigned int sleep(unsigned int secs) {
    espradio_task_delay(secs * 100);
    return 0;
}

int usleep(unsigned int us) {
    uint32_t ticks = us / 10000;
    if (ticks == 0) ticks = 1;
    espradio_task_delay(ticks);
    return 0;
}

void __assert_func(const char *file, int line, const char *func, const char *expr) {
    printf("ASSERT FAILED: %s:%d %s: %s\n", file, line, func ? func : "", expr ? expr : "");
    while (1) {}
}

/* ---------- FreeRTOS / IDF symbols needed by eloop & supplicant ---------- */

void vTaskDelay(uint32_t ticks) {
    espradio_task_delay(ticks);
}

int64_t esp_timer_get_time(void) {
    return (int64_t)espradio_time_us_now();
}

uint32_t esp_random(void) {
    uint32_t r;
    esp_fill_random(&r, sizeof(r));
    return r;
}

/* ---------- esp_wifi high-level wrappers → blob internals ---------- */

extern esp_err_t esp_wifi_connect_internal(void);
extern esp_err_t esp_wifi_disconnect_internal(void);

esp_err_t esp_wifi_connect(void) {
    return esp_wifi_connect_internal();
}

esp_err_t esp_wifi_disconnect(void) {
    return esp_wifi_disconnect_internal();
}


/* ---------- strrchr: not in picolibc, not in libwpa_supplicant ---------- */

char *strrchr(const char *s, int c) {
    const char *last = NULL;
    while (*s) {
        if (*s == (char)c) last = s;
        s++;
    }
    if ((char)c == '\0') return (char *)s;
    return (char *)last;
}
