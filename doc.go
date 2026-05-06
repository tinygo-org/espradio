// Package espradio provides wireless support for Espressif microcontrollers
// when used with the TinyGo compiler.
//
// The package wraps the Espressif Wi-Fi blobs and exposes both lower-level
// 1:1 bindings and higher-level Go APIs for common use cases such as:
//
//   - station and soft-AP Wi-Fi
//   - scanning
//   - raw Ethernet/netdev integration
//   - ESP-NOW datagram communication
//
// ESP-NOW can be used directly through the thin wrapper functions, or through
// the higher-level managed API returned by NewESPNow. The managed API maps each
// remote peer to a Peer that implements net.PacketConn.
//
// For applications that prefer a stream-like API over packet-oriented reads and
// writes, Peer can also be wrapped with PeerStream via peer.Stream().
//
// Example usage:
//
//	func main() {
//		if err := espradio.Enable(espradio.Config{}); err != nil {
//			panic(err)
//		}
//		if err := espradio.Start(); err != nil {
//			panic(err)
//		}
//
//		now, err := espradio.NewESPNow(espradio.ESPNowConfig{})
//		if err != nil {
//			panic(err)
//		}
//		defer now.Close()
//
//		peer, err := now.AddPeer(espradio.PeerConfig{
//			Address: espradio.ESPNowAddr{0x24, 0x6f, 0x28, 0xaa, 0xbb, 0xcc},
//			If:      espradio.WiFiInterfaceSTA,
//		})
//		if err != nil {
//			panic(err)
//		}
//		defer peer.Close()
//
//		if _, err := peer.WriteTo([]byte("hello"), nil); err != nil {
//			panic(err)
//		}
//
//		buf := make([]byte, espradio.ESPNowMaxDataLength)
//		n, addr, err := peer.ReadFrom(buf)
//		if err != nil {
//			panic(err)
//		}
//		println("received", n, "bytes from", addr.String())
//
//		stream := peer.Stream()
//		if _, err := stream.Write([]byte("streamed payload")); err != nil {
//			panic(err)
//		}
//
//		broadcast, err := now.Broadcast()
//		if err != nil {
//			panic(err)
//		}
//		if _, err := broadcast.WriteTo([]byte("announcement"), nil); err != nil {
//			panic(err)
//		}
//	}
package espradio // import "tinygo.org/x/espradio"
