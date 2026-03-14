#include <stdint.h>
#include <stddef.h>
#include <string.h>
#include <stdio.h>

#define ARENA_SIZE (80u * 1024u)
#define ARENA_ALIGN 8u

static uint8_t  s_arena[ARENA_SIZE] __attribute__((aligned(ARENA_ALIGN)));
static uint32_t s_arena_offset;

typedef struct free_node {
    struct free_node *next;
    uint32_t size;
} free_node_t;

static free_node_t *s_free_list;

static uint32_t align_up(uint32_t v, uint32_t a) {
    return (v + a - 1u) & ~(a - 1u);
}

void espradio_arena_free(void *p);

void *espradio_arena_alloc(size_t size) {
    if (size == 0) size = 1;
    size = align_up((uint32_t)size, ARENA_ALIGN);

    uint32_t total = size + sizeof(uint32_t);

    free_node_t **pp = &s_free_list;
    while (*pp) {
        free_node_t *node = *pp;
        if (node->size >= total) {
            *pp = node->next;
            uint32_t *hdr = (uint32_t *)node;
            *hdr = (uint32_t)size;
            return (void *)(hdr + 1);
        }
        pp = &node->next;
    }

    uint32_t off = align_up(s_arena_offset, ARENA_ALIGN);
    if (off + total > ARENA_SIZE) {
        printf("ARENA: OOM! need %lu, have %lu\n",
               (unsigned long)total, (unsigned long)(ARENA_SIZE - off));
        return NULL;
    }
    uint32_t *hdr = (uint32_t *)(s_arena + off);
    *hdr = (uint32_t)size;
    s_arena_offset = off + total;
    return (void *)(hdr + 1);
}

void *espradio_arena_calloc(size_t n, size_t size) {
    size_t total = n * size;
    void *p = espradio_arena_alloc(total);
    if (p) memset(p, 0, total);
    return p;
}

void *espradio_arena_realloc(void *ptr, size_t new_size) {
    if (!ptr) return espradio_arena_alloc(new_size);
    if (new_size == 0) {
        espradio_arena_free(ptr);
        return NULL;
    }

    uint32_t *hdr = ((uint32_t *)ptr) - 1;
    uint32_t old_size = *hdr;

    if (new_size <= old_size) return ptr;

    void *np = espradio_arena_alloc(new_size);
    if (np) {
        memcpy(np, ptr, old_size);
        espradio_arena_free(ptr);
    }
    return np;
}

void espradio_arena_free(void *p) {
    if (!p) return;
    uint32_t *hdr = ((uint32_t *)p) - 1;
    uint32_t size = *hdr;
    uint32_t total = size + sizeof(uint32_t);

    free_node_t *node = (free_node_t *)hdr;
    node->size = total;
    node->next = s_free_list;
    s_free_list = node;
}

void espradio_arena_stats(uint32_t *used, uint32_t *capacity) {
    if (used) *used = s_arena_offset;
    if (capacity) *capacity = ARENA_SIZE;
}
