package hwcrypto

import (
	"crypto/subtle"
	"errors"
	"unsafe"
)

const (
	gcmBlockSize = 16
	gcmNonceSize = 12 // RFC 8446 5.3.
	gcmTagSize   = 16
)

var errOpen = errors.New("hwcrypto: message authentication failed")

// block is an AES block encrypter. Calls to encrypt happen between begin and end.
type block interface {
	begin()
	encrypt(dst, src *[16]byte)
	end()
}

// gcm is AES-GCM with a 12 byte nonce and 16 byte tag, NIST SP 800-38D.
type gcm struct {
	b block
	t ghashTable
}

func (g *gcm) init(b block) {
	var H [16]byte
	g.b = b
	b.begin()
	b.encrypt(&H, &H)
	b.end()
	g.t.init(&H)
	clear(H[:])
}

func (g *gcm) zeroize() {
	*g = gcm{}
}

func (g *gcm) seal(dst, nonce, plaintext, ad []byte) []byte {
	if len(nonce) != gcmNonceSize {
		panic("hwcrypto: incorrect nonce length given to GCM")
	}
	ret, out := sliceForAppend(dst, len(plaintext)+gcmTagSize)
	if inexactOverlap(out, plaintext) {
		panic("hwcrypto: invalid buffer overlap of output and input")
	}
	if anyOverlap(out, ad) {
		panic("hwcrypto: invalid buffer overlap of output and additional data")
	}
	var counter, tagMask [16]byte
	copy(counter[:], nonce)
	counter[15] = 1
	g.b.begin()
	g.b.encrypt(&tagMask, &counter)
	counter[15] = 2
	g.counterCrypt(out, plaintext, &counter)
	g.b.end()
	g.tag(out[len(plaintext):], &tagMask, out[:len(plaintext)], ad)
	return ret
}

func (g *gcm) open(dst, nonce, ciphertext, ad []byte) ([]byte, error) {
	if len(nonce) != gcmNonceSize {
		panic("hwcrypto: incorrect nonce length given to GCM")
	}
	if len(ciphertext) < gcmTagSize {
		return nil, errOpen
	}
	tag := ciphertext[len(ciphertext)-gcmTagSize:]
	ciphertext = ciphertext[:len(ciphertext)-gcmTagSize]
	ret, out := sliceForAppend(dst, len(ciphertext))
	if inexactOverlap(out, ciphertext) {
		panic("hwcrypto: invalid buffer overlap of output and input")
	}
	if anyOverlap(out, ad) {
		panic("hwcrypto: invalid buffer overlap of output and additional data")
	}
	var counter, tagMask [16]byte
	var expected [gcmTagSize]byte
	copy(counter[:], nonce)
	counter[15] = 1
	g.b.begin()
	g.b.encrypt(&tagMask, &counter)
	g.b.end()
	g.tag(expected[:], &tagMask, ciphertext, ad)
	if subtle.ConstantTimeCompare(expected[:], tag) != 1 {
		return nil, errOpen
	}
	counter[15] = 2
	g.b.begin()
	g.counterCrypt(out, ciphertext, &counter)
	g.b.end()
	return ret, nil
}

// counterCrypt runs AES in counter mode with a 32 bit big endian counter.
func (g *gcm) counterCrypt(out, src []byte, counter *[16]byte) {
	var mask [16]byte
	for len(src) > 0 {
		g.b.encrypt(&mask, counter)
		putBE32(counter[12:], be32(counter[12:])+1)
		n := subtle.XORBytes(out, src, mask[:])
		out, src = out[n:], src[n:]
	}
	clear(mask[:])
}

// tag writes GHASH(ad, ciphertext) ^ tagMask to out.
func (g *gcm) tag(out []byte, tagMask *[16]byte, ciphertext, ad []byte) {
	var y fieldElement
	g.t.update(&y, ad)
	g.t.update(&y, ciphertext)
	y.low ^= uint64(len(ad)) * 8
	y.high ^= uint64(len(ciphertext)) * 8
	g.t.mul(&y)
	putBE32(out[0:], uint32(y.low>>32)^be32(tagMask[0:]))
	putBE32(out[4:], uint32(y.low)^be32(tagMask[4:]))
	putBE32(out[8:], uint32(y.high>>32)^be32(tagMask[8:]))
	putBE32(out[12:], uint32(y.high)^be32(tagMask[12:]))
}

func sliceForAppend(in []byte, n int) (head, tail []byte) {
	if total := len(in) + n; cap(in) >= total {
		head = in[:total]
	} else {
		head = make([]byte, total)
		copy(head, in)
	}
	tail = head[len(in):]
	return
}

func inexactOverlap(x, y []byte) bool {
	if len(x) == 0 || len(y) == 0 || &x[0] == &y[0] {
		return false
	}
	return anyOverlap(x, y)
}

func anyOverlap(x, y []byte) bool {
	return len(x) > 0 && len(y) > 0 &&
		uintptr(unsafe.Pointer(&x[0])) <= uintptr(unsafe.Pointer(&y[len(y)-1])) &&
		uintptr(unsafe.Pointer(&y[0])) <= uintptr(unsafe.Pointer(&x[len(x)-1]))
}

func be32(b []byte) uint32 {
	_ = b[3]
	return uint32(b[3]) | uint32(b[2])<<8 | uint32(b[1])<<16 | uint32(b[0])<<24
}

func putBE32(b []byte, v uint32) {
	_ = b[3]
	b[0], b[1], b[2], b[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
}

func le32(b []byte) uint32 {
	_ = b[3]
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func putLE32(b []byte, v uint32) {
	_ = b[3]
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}
