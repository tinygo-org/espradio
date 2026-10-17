package espradio

import (
	"context"
	"net/netip"
	"time"

	"github.com/soypat/lneto"
	"github.com/soypat/lneto/x/netdev"
	"github.com/soypat/lneto/x/xnet"
)

// RunStack runs stack over nd with a lneto netdev Runner until ctx is done.
// Start it on its own goroutine before using the stack.
// With the cores scheduler the whole loop runs on core 0, next to the blob.
func RunStack(ctx context.Context, nd *NetDev, stack *xnet.Netstack) (err error) {
	onCore0(func() { err = runStack(ctx, nd, stack) })
	return
}

func runStack(ctx context.Context, nd *NetDev, stack *xnet.Netstack) error {
	var iface netdev.Interface[STAConfig]
	if err := iface.Init(noLink{}, nd, netdev.InterfaceConfig{}); err != nil {
		return err
	}
	var runner netdev.Runner[STAConfig]
	err := runner.Configure(netdev.RunnerConfig[STAConfig]{
		Buffers: iface.RunnerBuffers(2),
		Backoff: pollBackoff,
		Flags:   netdev.RunnerInterfacePoll,
	})
	if err != nil {
		return err
	}
	return runner.Run(ctx, &iface, packets{stack})
}

func pollBackoff(uint) time.Duration { return 5 * time.Millisecond }

// packets gives the Runner only the packet methods of a Netstack. Putting the
// Netstack itself in an interface links in its whole socket layer, about 350 kB.
type packets struct{ ns *xnet.Netstack }

func (p packets) EgressPackets(bufs [][]byte, sizes []int, offset int) error {
	return p.ns.EgressPackets(bufs, sizes, offset)
}

func (p packets) IngressPackets(bufs [][]byte, offset int) error {
	return p.ns.IngressPackets(bufs, offset)
}

func (packets) EnableICMP(bool) error { return lneto.ErrUnsupported }

func (packets) EnableDHCP(context.Context, bool, netip.Addr) (netip.Addr, netip.Addr, int, error) {
	return netip.Addr{}, netip.Addr{}, 0, lneto.ErrUnsupported
}

func (packets) Socket(context.Context, string, int, int, netip.AddrPort, netip.AddrPort) (any, error) {
	return nil, lneto.ErrUnsupported
}

// noLink satisfies netdev.Interface, which requires a Netlink the Runner never uses.
type noLink struct{}

func (noLink) LinkConnect(STAConfig) error                 { return nil }
func (noLink) LinkDisconnect()                             {}
func (noLink) LinkNotify(netdev.NotifyCallback[STAConfig]) {}
