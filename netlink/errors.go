package netlink

import "errors"

var (
	errInvalidIPAddress = errors.New("invalid IP address")
	errEmptyHostname    = errors.New("empty hostname")
)
