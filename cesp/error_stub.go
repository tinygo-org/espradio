//go:build !(esp32c3 || esp32c3_qemu_target || esp32s3)

package cesp

const (
	espErrWifiBase    = int32(0x3000)
	espErrMemprotBase = int32(0xd000)
	espErrHwCrypto    = int32(0xc000)
	espErrFlash       = int32(0x10000)
	espErrMesh        = int32(0x4000)
	espErrNoMem       = int32(0x101)
	espErrInvalidArg  = int32(0x102)
	espErrTimeout     = int32(0x107)
)

func codeToErr(c int32) error {
	if c == 0 {
		return nil
	}
	return Error(c)
}
