//go:build esp32c3

#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "blobs/include/esp_timer.h"

#ifndef ESPRADIO_TIMER_SHIM_DEBUG
#define ESPRADIO_TIMER_SHIM_DEBUG 0
#endif

extern uint64_t espradio_time_us_now(void);

struct esp_timer {
    struct esp_timer *next;
    esp_timer_cb_t callback;
    void *arg;
    uint64_t period_us;
    uint64_t expiry_us;
    bool periodic;
    bool active;
};

static struct esp_timer *s_timer_list;

__attribute__((weak)) esp_err_t esp_timer_create(const esp_timer_create_args_t *create_args,
                                                 esp_timer_handle_t *out_handle) {
    if ((create_args == NULL) || (out_handle == NULL) || (create_args->callback == NULL)) {
        return ESP_ERR_INVALID_ARG;
    }
    void *espradio_arena_alloc(size_t);
    struct esp_timer *t = (struct esp_timer *)espradio_arena_alloc(sizeof(struct esp_timer));
    if (t == NULL) {
        return ESP_ERR_NO_MEM;
    }
    memset(t, 0, sizeof(*t));
    t->callback = create_args->callback;
    t->arg = create_args->arg;
    t->next = s_timer_list;
    s_timer_list = t;
    *out_handle = t;
    return ESP_OK;
}

__attribute__((weak)) esp_err_t esp_timer_start_once(esp_timer_handle_t timer, uint64_t timeout_us) {
    if (timer == NULL) {
        return ESP_ERR_INVALID_ARG;
    }
    struct esp_timer *t = (struct esp_timer *)timer;
    uint64_t now = espradio_time_us_now();
    t->periodic = false;
    t->period_us = timeout_us;
    t->expiry_us = now + timeout_us;
    t->active = true;
    if (timeout_us == 0 && t->callback != NULL) {
        t->callback(t->arg);
        t->active = false;
    }
    return ESP_OK;
}

__attribute__((weak)) esp_err_t esp_timer_start_periodic(esp_timer_handle_t timer, uint64_t period) {
    if ((timer == NULL) || (period == 0)) {
        return ESP_ERR_INVALID_ARG;
    }
    struct esp_timer *t = (struct esp_timer *)timer;
    uint64_t now = espradio_time_us_now();
    t->periodic = true;
    t->period_us = period;
    t->expiry_us = now + period;
    t->active = true;
    return ESP_OK;
}

__attribute__((weak)) esp_err_t esp_timer_restart(esp_timer_handle_t timer, uint64_t timeout_us) {
    if (timer == NULL) {
        return ESP_ERR_INVALID_ARG;
    }
    struct esp_timer *t = (struct esp_timer *)timer;
    uint64_t now = espradio_time_us_now();
    t->periodic = false;
    t->period_us = timeout_us;
    t->expiry_us = now + timeout_us;
    t->active = true;
    return ESP_OK;
}

__attribute__((weak)) esp_err_t esp_timer_stop(esp_timer_handle_t timer) {
    if (timer == NULL) {
        return ESP_ERR_INVALID_ARG;
    }
    struct esp_timer *t = (struct esp_timer *)timer;
    t->active = false;
    return ESP_OK;
}

__attribute__((weak)) esp_err_t esp_timer_delete(esp_timer_handle_t timer) {
    if (timer == NULL) {
        return ESP_ERR_INVALID_ARG;
    }
    struct esp_timer *t = (struct esp_timer *)timer;
    struct esp_timer **pp = &s_timer_list;
    while (*pp != NULL) {
        if (*pp == t) {
            *pp = t->next;
            break;
        }
        pp = &(*pp)->next;
    }
    void espradio_arena_free(void *);
    espradio_arena_free(t);
    return ESP_OK;
}

__attribute__((weak)) int64_t esp_timer_get_time(void) {
    return (int64_t)espradio_time_us_now();
}

__attribute__((weak)) bool esp_timer_is_active(esp_timer_handle_t timer) {
    if (timer == NULL) {
        return false;
    }
    return ((struct esp_timer *)timer)->active;
}

int espradio_esp_timer_poll_due(int max_fire) {
    if (max_fire <= 0) {
        return 0;
    }
    int fired = 0;
    uint64_t now = espradio_time_us_now();
    for (struct esp_timer *t = s_timer_list; t != NULL && fired < max_fire; t = t->next) {
        if ((!t->active) || (t->callback == NULL)) {
            continue;
        }
        if (now < t->expiry_us) {
            continue;
        }
        t->callback(t->arg);
        fired++;
        now = espradio_time_us_now();
        if (t->periodic) {
            t->expiry_us = now + t->period_us;
        } else {
            t->active = false;
        }
    }
    return fired;
}
