//go:build !esp32

package espradio

// makeArenaPool allocates the WiFi blob arena from the Go heap. On most targets
// this is fine because the arena is kept reachable via the arenaPool global.
func makeArenaPool(size int) []byte {
	return make([]byte, size)
}
