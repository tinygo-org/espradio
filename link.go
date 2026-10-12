//go:build esp32c3 || esp32c3_qemu_target || esp32s3 || esp32

package espradio

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/soypat/lneto/x/netdev"
)

var _ netdev.Netlink[STAConfig] = (*Link)(nil)

// Link manages the station connection. It enables and starts the radio on the
// first LinkConnect and only reconnects on later calls.
type Link struct {
	// Config is passed to Enable on the first LinkConnect.
	Config Config

	mu     sync.Mutex
	nd     *NetDev
	notify netdev.NotifyCallback[STAConfig]
	down   bool
}

// LinkConnect connects to the access point in cfg and blocks until it succeeds or fails.
func (l *Link) LinkConnect(cfg STAConfig) error {
	err := Enable(l.Config)
	if err != nil && !errors.Is(err, ErrAlreadyEnabled) {
		return err
	}
	if atomic.LoadUint32(&wifiStarted) == 0 {
		if err := Start(); err != nil {
			return err
		}
	}
	l.mu.Lock()
	l.down = false
	l.mu.Unlock()
	if err := Connect(cfg); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nd == nil {
		nd, err := StartNetDev()
		if err != nil {
			return err
		}
		l.nd = nd
	}
	return nil
}

// LinkDisconnect disconnects from the access point and stops any reconnect.
func (l *Link) LinkDisconnect() {
	l.mu.Lock()
	l.down = true
	l.mu.Unlock()
	Disconnect()
}

// LinkNotify sets the callback run after the link goes up or down outside LinkConnect.
func (l *Link) LinkNotify(cb netdev.NotifyCallback[STAConfig]) {
	l.mu.Lock()
	l.notify = cb
	l.mu.Unlock()
	SetLinkNotify(l.onLink)
}

// NetDev returns the Ethernet device, or nil before the first successful LinkConnect.
func (l *Link) NetDev() *NetDev {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.nd
}

func (l *Link) onLink(connected bool) {
	l.mu.Lock()
	cb, down := l.notify, l.down
	l.mu.Unlock()
	if cb == nil {
		return
	}
	retries, params := cb(connected)
	if connected || down {
		return
	}
	for i := 0; i < retries; i++ {
		if Connect(params) == nil {
			cb(true)
			return
		}
	}
}
