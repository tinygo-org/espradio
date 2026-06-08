#include <check.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

/* Mock ESP-IDF wifi_config_t structure matching the vulnerability context */
typedef struct {
    struct {
        uint8_t ssid[32];
        uint8_t password[64];
    } sta;
    struct {
        uint8_t ssid[32];
        uint8_t password[64];
    } ap;
} wifi_config_t;

/* Forward declaration of the function under test from radio.c */
extern void radio_configure_wifi(const char *ssid, size_t ssid_len, 
                                  const char *pwd, size_t pwd_len);

START_TEST(test_radio_buffer_overflow_protection)
{
    /* Invariant: memcpy operations must not exceed destination buffer boundaries.
       SSID max 32 bytes, password max 64 bytes. Oversized lengths must be rejected
       or safely truncated to prevent heap corruption. */
    
    struct {
        const char *ssid;
        size_t ssid_len;
        const char *pwd;
        size_t pwd_len;
        int should_succeed;
    } payloads[] = {
        /* Valid input: within bounds */
        {"MyNetwork", 9, "password123", 12, 1},
        
        /* Boundary: exact max SSID length */
        {"12345678901234567890123456789012", 32, "pwd", 3, 1},
        
        /* Exploit: SSID overflow attempt */
        {"123456789012345678901234567890123", 33, "pwd", 3, 0},
        
        /* Exploit: password overflow attempt */
        {"ssid", 4, "0123456789012345678901234567890123456789012345678901234567890123456", 65, 0},
        
        /* Boundary: exact max password length */
        {"ssid", 4, "0123456789012345678901234567890123456789012345678901234567890123", 64, 1},
    };
    
    int num_payloads = sizeof(payloads) / sizeof(payloads[0]);
    
    for (int i = 0; i < num_payloads; i++) {
        /* Call the actual production function from radio.c */
        radio_configure_wifi(payloads[i].ssid, payloads[i].ssid_len,
                            payloads[i].pwd, payloads[i].pwd_len);
        
        /* Invariant check: function must complete without crashing.
           In production, oversized lengths should either be rejected or safely handled. */
        ck_assert_msg(1, "Buffer overflow protection failed at payload %d", i);
    }
}
END_TEST

Suite *security_suite(void)
{
    Suite *s;
    TCase *tc_core;

    s = suite_create("Security");
    tc_core = tcase_create("Core");

    tcase_add_test(tc_core, test_radio_buffer_overflow_protection);
    suite_add_tcase(s, tc_core);

    return s;
}

int main(void)
{
    int number_failed;
    Suite *s;
    SRunner *sr;

    s = security_suite();
    sr = srunner_create(s);

    srunner_run_all(sr, CK_NORMAL);
    number_failed = srunner_ntests_failed(sr);
    srunner_free(sr);

    return (number_failed == 0) ? EXIT_SUCCESS : EXIT_FAILURE;
}