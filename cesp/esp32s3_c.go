//go:build esp32s3

package cesp

/*
#cgo CFLAGS: -Iblobs/include/esp32s3
#cgo LDFLAGS: -Lblobs/libs/esp32s3 -lcoexist -lcore -lmesh -lnet80211 -lespnow -lregulatory -lphy -lpp -lwpa_supplicant
*/
import "C"
