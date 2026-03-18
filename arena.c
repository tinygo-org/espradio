#include <stdint.h>
#include <stddef.h>
#include <string.h>
#include <stdio.h>
#include <stdlib.h>

void *espradio_arena_alloc(size_t size)              { return malloc(size); }
void *espradio_arena_calloc(size_t n, size_t size)   { return calloc(n, size); }
void  espradio_arena_free(void *p)                   { free(p); }

void *espradio_arena_realloc(void *ptr, size_t new_size) {
    if (!ptr) return malloc(new_size);
    if (new_size == 0) { free(ptr); return NULL; }
    void *np = malloc(new_size);
    if (np) {
        memcpy(np, ptr, new_size);
        free(ptr);
    }
    return np;
}

void espradio_arena_stats(uint32_t *used, uint32_t *capacity) {
    if (used) *used = 0;
    if (capacity) *capacity = 0;
}
