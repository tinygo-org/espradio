// Package hwcrypto provides the TLS 1.3 suites TLS_AES_128_GCM_SHA256 and
// TLS_AES_256_GCM_SHA384 for tlsauto. AES runs on the AES accelerator of the
// ESP32, ESP32-C3, ESP32-S3 and ESP32-C6. SHA-256 runs on the SHA accelerator of
// the C3, S3 and C6, and SHA-384 on the S3. Other targets and hashes use lcrypto.
package hwcrypto
