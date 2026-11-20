//go:build esp32c3 || esp32s3 || esp32c6

package hwcrypto

import (
	"device/esp"
	"hash"
	"runtime/volatile"
	"unsafe"
)

// SHA_MODE values, ESP-IDF components/soc/esp32s3/include/soc/hwcrypto_reg.h.
const (
	shaMode256 = 2
	shaMode384 = 3
)

var (
	shaH = (*[16]volatile.Register32)(unsafe.Pointer(&esp.SHA.H_MEM))
	shaM = (*[32]volatile.Register32)(unsafe.Pointer(&esp.SHA.M_MEM))
)

// digest runs SHA-2 on the SHA accelerator. The hash state is kept here and
// loaded into H_MEM for each Write, so several digests can be used at once.
type digest struct {
	state     [16]uint32
	buf       [128]byte
	nbuf      int
	len       uint64
	started   bool
	mode      uint32
	size      int
	blockSize int
	stateLen  int
}

func (d *digest) Size() int      { return d.size }
func (d *digest) BlockSize() int { return d.blockSize }

func (d *digest) Reset() {
	d.state = [16]uint32{}
	d.nbuf = 0
	d.len = 0
	d.started = false
}

func (d *digest) Write(p []byte) (int, error) {
	n := len(p)
	d.len += uint64(n)
	if d.nbuf > 0 {
		c := copy(d.buf[d.nbuf:d.blockSize], p)
		d.nbuf += c
		p = p[c:]
		if d.nbuf < d.blockSize {
			return n, nil
		}
		d.blocks(d.buf[:d.blockSize])
		d.nbuf = 0
	}
	if full := len(p) - len(p)%d.blockSize; full > 0 {
		d.blocks(p[:full])
		p = p[full:]
	}
	d.nbuf = copy(d.buf[:], p)
	return n, nil
}

// Sum appends the digest to b without changing d.
func (d *digest) Sum(b []byte) []byte {
	c := *d
	lenSize := d.blockSize / 8
	var tail [128 + 16]byte
	tail[0] = 0x80
	pad := d.blockSize - (int(c.len%uint64(d.blockSize))+1+lenSize)%d.blockSize
	if pad == d.blockSize {
		pad = 0
	}
	n := 1 + pad + lenSize
	bits := c.len * 8
	for i := 0; i < 8; i++ {
		tail[n-1-i] = byte(bits >> (8 * i))
	}
	c.Write(tail[:n])
	var out [64]byte
	for i := 0; i < d.size/4; i++ {
		putLE32(out[i*4:], c.state[i])
	}
	c.state = [16]uint32{}
	c.buf = [128]byte{}
	return append(b, out[:d.size]...)
}

// blocks hashes whole blocks. The words are written in memory order,
// ESP-IDF components/hal/esp32c3/include/hal/sha_ll.h sha_ll_fill_text_block.
func (d *digest) blocks(p []byte) {
	lock()
	enable()
	for esp.SHA.BUSY.Get() != 0 {
	}
	if d.started {
		for i := 0; i < d.stateLen; i++ {
			shaH[i].Set(d.state[i])
		}
	}
	words := d.blockSize / 4
	for len(p) > 0 {
		for i := 0; i < words; i++ {
			shaM[i].Set(le32(p[i*4:]))
		}
		esp.SHA.MODE.Set(d.mode)
		if d.started {
			esp.SHA.CONTINUE.Set(1)
		} else {
			esp.SHA.START.Set(1)
			d.started = true
		}
		for esp.SHA.BUSY.Get() != 0 {
		}
		p = p[d.blockSize:]
	}
	for i := 0; i < d.stateLen; i++ {
		d.state[i] = shaH[i].Get()
	}
	unlock()
}

// SHA256 is SHA-256 on the SHA accelerator. It implements hash.Hash.
type SHA256 struct{ digest }

// NewSHA256 returns a new SHA-256 hash.
func NewSHA256() *SHA256 {
	return &SHA256{digest{mode: shaMode256, size: 32, blockSize: 64, stateLen: 8}}
}

func newSHA256() hash.Hash { return NewSHA256() }
