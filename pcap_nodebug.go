//go:build !pcapdebug

package espradio

const pcapdebug = false

// printPacket prints packets when pcapdebug build tag is set.
func printPacket(msg string, frame []byte) {}
