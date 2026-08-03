package netlink

import (
	"context"
	"errors"
	"net"
	"net/netip"

	"github.com/soypat/lneto/http/httphi"
	"github.com/soypat/lneto/x/xnet"
)

// ListenAndServe registers a TCP listener onto the port and begins receiving connections
// indefinetely. It will return if Listener returns an error or if Router is closed.
func (link *Esplink) ListenAndServe(router *httphi.Router, port uint16) error {
	// Listen by asking the lneto stack for a socket directly instead of going
	// through stdlib net.Listen and the netdev file descriptor layer due to a bug.
	stack := link.StackGo()
	laddr := netip.AddrPortFrom(netip.Addr{}, port)
	sock, err := stack.SocketNetip(context.Background(), "tcp4", xnet.AF_INET, xnet.SOCK_STREAM, laddr, netip.AddrPort{})
	if err != nil {
		return err
	}
	listener, ok := sock.(net.Listener)
	if !ok {
		return errors.New("stack returned non-listener socket")
	}
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		err = router.Handle(conn)
		if err != nil {
			conn.Close()
			if err == net.ErrClosed {
				return err
			}
			println("httphi.Router failed to handle connection: ", err.Error())
		}
	}
}
