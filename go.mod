module tinygo.org/x/espradio

go 1.24.4

require (
	github.com/soypat/lneto v0.1.1-0.20260425023453-aa77403a2b32
	tinygo.org/x/drivers v0.35.0
)

require github.com/soypat/natiu-mqtt v0.6.0

// Local replace for development: points to the lneto netdev branch which adds
// the x/netdev package used by examples/esp32-netdev. Remove once that branch
// is merged and a tagged release is cut.
replace github.com/soypat/lneto => ../lneto
