//go:build esp32c3 || esp32c3_qemu_target || esp32s3 || esp32

package espradio

// LockToCore0 pins the calling goroutine to core 0, so its radio calls run directly instead of
// on a new goroutine each time. Call UnlockFromCore0 before the goroutine exits.
func LockToCore0() { pinCore0() }

// UnlockFromCore0 undoes LockToCore0. The goroutine stays on core 0.
func UnlockFromCore0() { unpinCore0() }
