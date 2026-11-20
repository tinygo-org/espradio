//go:build (esp32 || esp32c3 || esp32s3 || esp32c6) && scheduler.cores

package hwcrypto

import "sync"

var mu sync.Mutex

func lock()   { mu.Lock() }
func unlock() { mu.Unlock() }
