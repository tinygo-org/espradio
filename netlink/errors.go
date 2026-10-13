package netlink

import "errors"

var (
	errInvalidIPAddress = errors.New("invalid IP address")
	errEmptyHostname    = errors.New("empty hostname")
	errNoAddress        = errors.New("no address for host")
)
