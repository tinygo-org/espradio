//go:build esp32c3

package cesp

/*
#cgo CFLAGS: -Iblobs/include/esp32c3
#cgo LDFLAGS: -Lblobs/libs/esp32c3 -lcoexist -lcore -lmesh -lnet80211 -lespnow -lregulatory -lphy -lpp -lwpa_supplicant
*/
import "C"
