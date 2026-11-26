//go:build (esp32 || esp32c3 || esp32s3 || esp32c6) && !scheduler.cores

package hwcrypto

// The accelerators are used without yielding, so goroutines on one core cannot interleave.
func lock()   {}
func unlock() {}
