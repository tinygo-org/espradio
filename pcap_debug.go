//go:build pcapdebug

package espradio

import (
	"machine"
	"time"

	"github.com/soypat/lneto/x/xnet"
)

const pcapdebug = true

var pcap xnet.CapturePrinter
var _ = pcap.Configure(machine.Serial, xnet.CapturePrinterConfig{
	NamespaceWidth: 3, // "IN ", "OUT"
	TimePrecision:  3,
	Now:            time.Now,
})

func printPacket(msg string, frame []byte) {
	pcap.PrintPacket(msg, frame)
}
