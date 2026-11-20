// This example checks the hwcrypto suites against lcrypto and times both.
// Time the start and end lines on the host, as time.Now is not reliable during long computations.
//
// tinygo flash -target xiao-esp32c3 -monitor ./examples/hwcrypto-bench
package main

import (
	"bytes"
	"encoding/hex"
	"hash"
	"math/rand"
	"time"

	"github.com/soypat/lcrypto/aesgcm"
	"github.com/soypat/lcrypto/sha256"
	"github.com/soypat/lcrypto/sha512"
	"github.com/soypat/lneto/crypto/tlsauto"
	"tinygo.org/x/espradio/hwcrypto"
)

var (
	rng    = rand.New(rand.NewSource(1))
	data   [16384 + 16]byte
	out    [16384 + 16]byte
	ok     = true
	rounds = 64
	suite  = hwcrypto.TLSAES128GCMSHA256()
	s384   = hwcrypto.TLSAES256GCMSHA384()
)

func main() {
	time.Sleep(2 * time.Second)
	println("hwcrypto-bench")

	checkHash("sha256 abc", suite.NewHash(), "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
	checkHash("sha256 2 blocks", suite.NewHash(), "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq",
		"248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1")
	checkHash("sha384 abc", s384.NewHash(), "abc",
		"cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7")
	crossHash("sha256", suite.NewHash, func() hash.Hash { return sha256.New() })
	crossHash("sha384", s384.NewHash, func() hash.Hash { return sha512.New384() })
	crossAEAD(16)
	crossAEAD(32)
	if ok {
		println("all checks passed")
	}

	rng.Read(data[:])
	for {
		benchAEAD("hw aesgcm", suite.NewAEAD())
		benchAEAD("sw aesgcm", new(aesgcm.Cipher))
		benchHash("hw sha256", suite.NewHash())
		benchHash("sw sha256", sha256.New())
		benchHash("hw sha384", s384.NewHash())
		benchHash("sw sha384", sha512.New384())
		println("--------")
		time.Sleep(5 * time.Second)
	}
}

func fail(msg string) {
	ok = false
	println("FAIL", msg)
}

func checkHash(name string, h hash.Hash, msg, want string) {
	h.Write([]byte(msg))
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		fail(name + " got " + got)
	}
}

// crossHash writes random chunks to two interleaved hardware hashes and one
// software hash and calls Sum along the way, as the TLS transcript does.
func crossHash(name string, hw func() hash.Hash, sw func() hash.Hash) {
	a, b, s := hw(), hw(), sw()
	var buf [300]byte
	for i := 0; i < 200; i++ {
		n := rng.Intn(len(buf))
		rng.Read(buf[:n])
		a.Write(buf[:n])
		b.Write(buf[:n])
		s.Write(buf[:n])
		want := s.Sum(nil)
		if !bytes.Equal(a.Sum(nil), want) || !bytes.Equal(b.Sum(nil), want) {
			fail(name + " mismatch")
			return
		}
	}
	a.Reset()
	s.Reset()
	if !bytes.Equal(a.Sum(nil), s.Sum(nil)) {
		fail(name + " empty mismatch")
	}
}

func crossAEAD(keyLen int) {
	key := make([]byte, keyLen)
	nonce := make([]byte, 12)
	ad := make([]byte, 13)
	hw := suite.NewAEAD()
	var sw aesgcm.Cipher
	for i := 0; i < 100; i++ {
		rng.Read(key)
		rng.Read(nonce)
		rng.Read(ad)
		n := rng.Intn(2000)
		if i == 0 {
			n = 16384
		}
		rng.Read(data[:n])
		hw.Rekey(key)
		sw.Rekey(key)
		want := sw.Seal(nil, nonce, data[:n], ad)
		got := hw.Seal(out[:0], nonce, data[:n], ad)
		if !bytes.Equal(got, want) {
			fail("aesgcm seal mismatch")
			return
		}
		back, err := hw.Open(out[:0], nonce, got, ad)
		if err != nil || !bytes.Equal(back, data[:n]) {
			fail("aesgcm open failed")
			return
		}
		want[0] ^= 1
		if _, err := hw.Open(nil, nonce, want, ad); err == nil {
			fail("aesgcm accepted a tampered record")
			return
		}
	}
}

func benchAEAD(name string, c tlsauto.AEADCipher) {
	key := make([]byte, 16)
	nonce := make([]byte, 12)
	c.Rekey(key)
	println("start", name, "seal", rounds, "x 16 KB")
	for i := 0; i < rounds; i++ {
		c.Seal(out[:0], nonce, data[:16384], nonce)
	}
	println("end", name)
}

func benchHash(name string, h hash.Hash) {
	var sum [64]byte
	println("start", name, rounds, "x 16 KB")
	for i := 0; i < rounds; i++ {
		h.Reset()
		h.Write(data[:16384])
		h.Sum(sum[:0])
	}
	println("end", name)
}
