#include <stdint.h>
#include <stddef.h>
#include <string.h>
#include <stdio.h>

/* Arena allocator: boundary-tag free-list allocator inside a Go-allocated pool.
 *
 * TinyGo's malloc IS the Go GC allocator (picolibc's malloc/sbrk are not
 * compiled).  The WiFi blob stores allocated pointers in ROM BSS which the
 * GC cannot scan, so individual malloc'd objects get collected → crash.
 *
 * Solution: Go allocates one large []byte (kept alive by a package-level
 * variable) and passes its backing array here.  All blob allocations are
 * sub-allocated from this pool using a first-fit free-list with boundary
 * tags for O(1) coalescing on free.
 *
 * Block layout:
 *   [header: size_t]  payload ...  [footer: size_t]
 * The size field = total block size (header+payload+footer).
 * Low bit of header = 1 means allocated, 0 means free.
 * Free blocks additionally store a next pointer in the first sizeof(void*)
 * bytes of the payload area.
 *
 * Minimum block size = HDR + max(sizeof(void*), ALIGN) + FTR.
 */

#define ALIGN      8
#define HDR        sizeof(size_t)
#define FTR        sizeof(size_t)
#define OVERHEAD   (HDR + FTR)
#define MIN_PAYLOAD (sizeof(void *) > ALIGN ? sizeof(void *) : ALIGN)
#define MIN_BLOCK  (OVERHEAD + MIN_PAYLOAD)
#define ALLOC_BIT  ((size_t)1)

/* Access helpers */
#define BLK_HDR(b)       (*(size_t *)(b))
#define BLK_SIZE(b)      (BLK_HDR(b) & ~ALLOC_BIT)
#define BLK_ALLOC(b)     (BLK_HDR(b) & ALLOC_BIT)
#define BLK_FTR(b)       (*(size_t *)((uint8_t *)(b) + BLK_SIZE(b) - FTR))
#define BLK_PAYLOAD(b)   ((void *)((uint8_t *)(b) + HDR))
#define PAYLOAD_BLK(p)   ((uint8_t *)(p) - HDR)
#define BLK_NEXT(b)      ((uint8_t *)((uint8_t *)(b) + BLK_SIZE(b)))
/* Free-list next pointer stored at start of payload */
#define FREE_NEXT(b)     (*(uint8_t **)((uint8_t *)(b) + HDR))

static uint8_t *arena_base;
static size_t   arena_cap;
static uint8_t *free_list;   /* singly-linked list of free blocks */

#if 0
#define ARENA_DBG(...) printf(__VA_ARGS__)
#else
#define ARENA_DBG(...) ((void)0)
#endif

void espradio_arena_init(uint8_t *base, size_t cap) {
    arena_base = base;
    arena_cap  = cap;

    /* Align base forward */
    uintptr_t misalign = ((uintptr_t)base) & (ALIGN - 1);
    if (misalign) {
        size_t pad = ALIGN - misalign;
        base += pad;
        cap  -= pad;
    }

    /* Round cap down to alignment */
    cap &= ~(size_t)(ALIGN - 1);

    arena_base = base;
    arena_cap  = cap;

    /* Initialise entire pool as one free block */
    BLK_HDR(base) = cap;  /* size, ALLOC_BIT=0 → free */
    BLK_FTR(base) = cap;
    FREE_NEXT(base) = NULL;
    free_list = base;
}

void *espradio_arena_alloc(size_t size) {
    if (size == 0) size = 1;
    /* Round payload up to alignment */
    size = (size + ALIGN - 1) & ~(size_t)(ALIGN - 1);
    size_t need = size + OVERHEAD;
    if (need < MIN_BLOCK) need = MIN_BLOCK;

    /* First-fit search */
    uint8_t **prev = &free_list;
    uint8_t  *blk  = free_list;
    while (blk) {
        size_t bsz = BLK_SIZE(blk);
        if (bsz >= need) {
            /* Try to split */
            size_t rem = bsz - need;
            if (rem >= MIN_BLOCK) {
                /* Shrink this free block, allocate from the end */
                BLK_HDR(blk) = rem;       /* free block is now smaller */
                BLK_FTR(blk) = rem;
                uint8_t *alloc_blk = blk + rem;
                BLK_HDR(alloc_blk) = need | ALLOC_BIT;
                BLK_FTR(alloc_blk) = need | ALLOC_BIT;
                ARENA_DBG("arena: alloc %zu @ %p (split, free_rem=%zu)\n",
                          size, BLK_PAYLOAD(alloc_blk), rem);
                return BLK_PAYLOAD(alloc_blk);
            } else {
                /* Use entire block */
                *prev = FREE_NEXT(blk);
                BLK_HDR(blk) = bsz | ALLOC_BIT;
                BLK_FTR(blk) = bsz | ALLOC_BIT;
                ARENA_DBG("arena: alloc %zu @ %p (exact %zu)\n",
                          size, BLK_PAYLOAD(blk), bsz);
                return BLK_PAYLOAD(blk);
            }
        }
        prev = (uint8_t **)((uint8_t *)blk + HDR); /* &FREE_NEXT(blk) */
        blk = FREE_NEXT(blk);
    }
    ARENA_DBG("arena: alloc %zu FAILED (OOM)\n", size);
    /* Use puts instead of printf to avoid printf→malloc recursion via --wrap */
    puts("arena: OOM");
    return NULL;
}

void *espradio_arena_calloc(size_t n, size_t size) {
    size_t total = n * size;
    void *p = espradio_arena_alloc(total);
    if (p) memset(p, 0, total);
    return p;
}

void espradio_arena_free(void *p) {
    if (!p) return;
    uint8_t *blk = PAYLOAD_BLK(p);

    /* Reject pointers outside the arena pool.  With --wrap=free on
     * Xtensa, ALL free() calls arrive here — including frees of
     * non-arena memory (picolibc internals, ROM buffers, etc.).
     * Without this check the coalescing code below would interpret
     * random memory as block headers and corrupt the Go heap. */
    if (blk < arena_base || blk >= arena_base + arena_cap) {
        ARENA_DBG("arena: free %p OUTSIDE pool [%p, %p) — ignored\n",
                  p, arena_base, arena_base + arena_cap);
        return;
    }

    size_t bsz = BLK_SIZE(blk);

    /* Sanity-check the block header. */
    if (bsz == 0 || bsz > arena_cap || blk + bsz > arena_base + arena_cap) {
        ARENA_DBG("arena: free %p corrupt header (bsz=%zu) — ignored\n", p, bsz);
        return;
    }

    ARENA_DBG("arena: free %p (blk %p, size %zu)\n", p, blk, bsz);

    /* Coalesce with next block */
    uint8_t *next = blk + bsz;
    if (next < arena_base + arena_cap && !BLK_ALLOC(next)) {
        /* Remove next from free list */
        uint8_t **pp = &free_list;
        while (*pp && *pp != next) pp = (uint8_t **)(*pp + HDR);
        if (*pp == next) *pp = FREE_NEXT(next);
        bsz += BLK_SIZE(next);
    }

    /* Coalesce with previous block */
    if (blk > arena_base) {
        size_t prev_sz = *(size_t *)(blk - FTR) & ~ALLOC_BIT;
        uint8_t *prev_blk = blk - prev_sz;
        if (prev_blk >= arena_base && !BLK_ALLOC(prev_blk)) {
            /* Remove prev from free list */
            uint8_t **pp = &free_list;
            while (*pp && *pp != prev_blk) pp = (uint8_t **)(*pp + HDR);
            if (*pp == prev_blk) *pp = FREE_NEXT(prev_blk);
            blk = prev_blk;
            bsz += BLK_SIZE(prev_blk);
        }
    }

    /* Mark free and add to front of free list */
    BLK_HDR(blk) = bsz;           /* ALLOC_BIT=0 */
    BLK_FTR(blk) = bsz;
    FREE_NEXT(blk) = free_list;
    free_list = blk;
}

void *espradio_arena_realloc(void *ptr, size_t new_size) {
    if (!ptr) return espradio_arena_alloc(new_size);
    if (new_size == 0) { espradio_arena_free(ptr); return NULL; }

    uint8_t *blk = PAYLOAD_BLK(ptr);
    /* Reject non-arena pointers (see espradio_arena_free comment). */
    if (blk < arena_base || blk >= arena_base + arena_cap) {
        return espradio_arena_alloc(new_size);
    }
    size_t old_total = BLK_SIZE(blk);
    if (old_total == 0 || old_total > arena_cap || blk + old_total > arena_base + arena_cap) {
        return espradio_arena_alloc(new_size);
    }
    size_t old_payload = old_total - OVERHEAD;

    void *np = espradio_arena_alloc(new_size);
    if (np) {
        memcpy(np, ptr, old_payload < new_size ? old_payload : new_size);
        espradio_arena_free(ptr);
    }
    return np;
}

void espradio_arena_stats(uint32_t *used, uint32_t *capacity) {
    /* Walk all blocks to compute used bytes */
    uint32_t u = 0;
    uint8_t *blk = arena_base;
    uint8_t *end = arena_base + arena_cap;
    while (blk < end) {
        size_t bsz = BLK_SIZE(blk);
        if (bsz == 0 || bsz > (size_t)(end - blk)) break;
        if (BLK_ALLOC(blk)) u += (uint32_t)bsz;
        blk += bsz;
    }
    if (used)     *used     = u;
    if (capacity) *capacity = (uint32_t)arena_cap;
}

#ifdef __XTENSA__
/* --wrap redirects (ESP32-S3): catch direct malloc calls from blob .a */
void *__wrap_malloc(size_t size)              { return espradio_arena_alloc(size); }
void *__wrap_calloc(size_t n, size_t size)    { return espradio_arena_calloc(n, size); }
void  __wrap_free(void *p)                    { espradio_arena_free(p); }
void *__wrap_realloc(void *p, size_t size)    { return espradio_arena_realloc(p, size); }
#endif
