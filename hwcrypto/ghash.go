// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Ported from the 4 bit table GHASH in Go 1.20 src/crypto/cipher/gcm.go.

package hwcrypto

// fieldElement is an element of GF(2^128) in GCM bit order.
type fieldElement struct {
	low, high uint64
}

// ghashTable holds the 16 multiples of H, indexed with the bits reversed.
// Lookups depend on secret data. The table lives in internal SRAM, which has
// no data cache on the ESP32-C3 and ESP32-S3, so the access time is fixed.
type ghashTable [16]fieldElement

func (t *ghashTable) init(H *[16]byte) {
	x := fieldElement{be64(H[:8]), be64(H[8:])}
	t[reverseBits(1)] = x
	for i := 2; i < 16; i += 2 {
		t[reverseBits(i)] = gcmDouble(&t[reverseBits(i/2)])
		d := &t[reverseBits(i)]
		t[reverseBits(i+1)] = fieldElement{d.low ^ x.low, d.high ^ x.high}
	}
}

func reverseBits(i int) int {
	i = ((i << 2) & 0xc) | ((i >> 2) & 0x3)
	i = ((i << 1) & 0xa) | ((i >> 1) & 0x5)
	return i
}

// gcmDouble is a right shift in GCM bit order, reduced by 1+x+x^2+x^7+x^128.
func gcmDouble(x *fieldElement) (d fieldElement) {
	msb := x.high & 1
	d.high = x.high>>1 | x.low<<63
	d.low = x.low >> 1
	d.low ^= -msb & 0xe100000000000000
	return d
}

// reduce4 is the reduction table of Go 1.20 gcmReductionTable computed from
// its four basis entries, so no lookup depends on secret data.
func reduce4(m uint64) uint64 {
	return (-(m & 1) & 0x1c20) ^ (-(m >> 1 & 1) & 0x3840) ^ (-(m >> 2 & 1) & 0x7080) ^ (-(m >> 3 & 1) & 0xe100)
}

// mul sets y to y*H.
func (t *ghashTable) mul(y *fieldElement) {
	var z fieldElement
	for i := 0; i < 2; i++ {
		word := y.high
		if i == 1 {
			word = y.low
		}
		for j := 0; j < 64; j += 4 {
			msw := z.high & 0xf
			z.high = z.high>>4 | z.low<<60
			z.low >>= 4
			z.low ^= reduce4(msw) << 48
			e := &t[word&0xf]
			z.low ^= e.low
			z.high ^= e.high
			word >>= 4
		}
	}
	*y = z
}

// update absorbs data into y, zero padding the last partial block.
func (t *ghashTable) update(y *fieldElement, data []byte) {
	for len(data) >= 16 {
		y.low ^= be64(data)
		y.high ^= be64(data[8:])
		t.mul(y)
		data = data[16:]
	}
	if len(data) > 0 {
		var partial [16]byte
		copy(partial[:], data)
		y.low ^= be64(partial[:])
		y.high ^= be64(partial[8:])
		t.mul(y)
	}
}

func be64(b []byte) uint64 {
	return uint64(be32(b))<<32 | uint64(be32(b[4:]))
}
