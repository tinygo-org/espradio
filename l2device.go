package espradio

const EthMTU = 1500

// L2Device is the interface a TCP/IP stack needs from a link-layer device.
//
//	espradio.L2Device  (L2: raw Ethernet frames)
//	       ↓
//	  NetStack          (L3/L4: ARP, DHCP, IP, TCP, UDP, DNS)
//	       ↓
//	  implements Netdever → Use() → net.Dial / http.Get work
type L2Device interface {
	SendEth(frame []byte) error
	RecvCh() <-chan []byte
	HardwareAddr() ([6]byte, error)
	MTU() int
}
