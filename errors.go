package espradio

// Error is an error from the radio stack.
type Error int32

func (e Error) Error() string {
	return e.message()
}
