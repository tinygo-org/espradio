/* Fixed-arg shims that satisfy the externals declared by libprintf.a.
 *
 * Background: libprintf.a (the Espressif blob) ships printf / vprintf /
 * vsnprintf / sprintf / snprintf / pp_printf / phy_printf / net80211_printf /
 * coexist_printf / rtc_printf / syslog and the OSI _log_write / _log_writev
 * helpers as STRONG symbols. Its varargs handling is incompatible with
 * Clang-built call sites on Xtensa (GCC vs Clang vararg ABI mismatch),
 * which is why %lu / %u / %lx printed garbage and crashed at LogLevelDebug.
 *
 * The fix is the one esp-rs uses: keep libprintf.a, but never call its
 * varargs entry points from Clang code. All blob -> libprintf calls stay
 * GCC -> GCC and work correctly. Our only job is to satisfy the two
 * fixed-arg externals libprintf needs (_putchar, __esp_radio_printf), so
 * libprintf has somewhere to send its output. From our side, all
 * diagnostic prints go through ets_printf (ROM, %d/%x/%p/%s/%c) instead.
 */

#include <stdint.h>

extern int  uart_tx_one_char(uint8_t c);
extern int  ets_printf(const char *fmt, ...);

/* Per-character sink used by libprintf's vsnprintf/printf/etc. internals.
 * Fixed-arg, no varargs: safe to call from Clang. */
void _putchar(int c) {
    uart_tx_one_char((uint8_t)c);
}

/* libprintf calls __esp_radio_printf(tag, msg) once per fully-formatted
 * log line.  Both arguments are NUL-terminated strings — no varargs.
 * We forward to ets_printf with %s only, which is varargs-safe. */
void __esp_radio_printf(const char *tag, const char *msg) {
    if (tag && tag[0]) {
        ets_printf("[%s] %s", tag, msg);
    } else {
        ets_printf("%s", msg ? msg : "");
    }
}
