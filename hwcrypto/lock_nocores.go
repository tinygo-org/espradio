//go:build (esp32c3 || esp32s3) && !scheduler.cores

package hwcrypto

// The accelerators are used without yielding, so goroutines on one core cannot interleave.
func lock()   {}
func unlock() {}
