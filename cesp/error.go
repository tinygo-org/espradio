package cesp

import "strconv"

// Error is an ESP-IDF error code.
type Error int32

// EspErrTimeout is returned when an operation times out.
const EspErrTimeout = Error(espErrTimeout)

func (e Error) Error() string {
	switch {
	case int32(e) >= espErrMemprotBase:
		return "espradio: memprot error " + strconv.FormatInt(int64(int32(e)), 10)
	case int32(e) >= espErrHwCrypto:
		return "espradio: unknown hw crypto error"
	case int32(e) >= espErrFlash:
		return "espradio: unknown flash error"
	case int32(e) >= espErrMesh:
		return "espradio: unknown mesh error"
	case int32(e) >= espErrWifiBase:
		code := int32(e)
		switch code {
		case 0x3001:
			return "espradio: wifi not initialized (driver was not installed by esp_wifi_init)"
		case 0x3002:
			return "espradio: wifi not started (call esp_wifi_start)"
		default:
			return "espradio: wifi error " + strconv.FormatInt(int64(code), 10)
		}
	default:
		switch int32(e) {
		case 0:
			return "espradio: no error"
		case espErrNoMem:
			return "espradio: no memory"
		case espErrInvalidArg:
			return "espradio: invalid argument"
		case espErrTimeout:
			return "espradio: timeout"
		case 2:
			return "espradio: auth expired"
		case 15:
			return "espradio: 4-way handshake timeout"
		case 201:
			return "espradio: AP not found"
		case 202:
			return "espradio: auth failed"
		case 203:
			return "espradio: assoc failed"
		case 204:
			return "espradio: handshake timeout"
		case 205:
			return "espradio: connection failed"
		default:
			return "espradio: error " + strconv.FormatInt(int64(int32(e)), 10)
		}
	}
}
