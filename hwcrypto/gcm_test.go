package hwcrypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"math/rand"
	"testing"
)

type swBlock struct{ b cipher.Block }

func (s swBlock) begin()                     {}
func (s swBlock) end()                       {}
func (s swBlock) encrypt(dst, src *[16]byte) { s.b.Encrypt(dst[:], src[:]) }

func newTestGCM(t *testing.T, key []byte) (*gcm, cipher.AEAD) {
	b, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	std, err := cipher.NewGCM(b)
	if err != nil {
		t.Fatal(err)
	}
	g := new(gcm)
	g.init(swBlock{b})
	return g, std
}

func unhex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// Test case 4 of the GCM spec, McGrew and Viega, "The Galois/Counter Mode of Operation", Appendix B.
func TestGCMVector(t *testing.T) {
	g, _ := newTestGCM(t, unhex("feffe9928665731c6d6a8f9467308308"))
	nonce := unhex("cafebabefacedbaddecaf888")
	pt := unhex("d9313225f88406e5a55909c5aff5269a86a7a9531534f7da2e4c303d8a318a721c3c0c95956809532fcf0e2449a6b525b16aedf5aa0de657ba637b39")
	ad := unhex("feedfacedeadbeeffeedfacedeadbeefabaddad2")
	want := unhex("42831ec2217774244b7221b784d0d49ce3aa212f2c02a4e035c17e2329aca12e21d514b25466931c7d8f6a5aac84aa051ba30b396a0aac973d58e091" +
		"5bc94fbc3221a5db94fae95ae7121a47")
	got := g.seal(nil, nonce, pt, ad)
	if !bytes.Equal(got, want) {
		t.Fatalf("seal = %x, want %x", got, want)
	}
	back, err := g.open(nil, nonce, got, ad)
	if err != nil || !bytes.Equal(back, pt) {
		t.Fatalf("open = %x, %v", back, err)
	}
}

func TestGCMMatchesStdlib(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, keyLen := range []int{16, 32} {
		key := make([]byte, keyLen)
		rng.Read(key)
		g, std := newTestGCM(t, key)
		for _, n := range []int{0, 1, 15, 16, 17, 31, 64, 100, 1000, 16384} {
			for _, adLen := range []int{0, 5, 13, 16, 40} {
				nonce := make([]byte, gcmNonceSize)
				pt := make([]byte, n)
				ad := make([]byte, adLen)
				rng.Read(nonce)
				rng.Read(pt)
				rng.Read(ad)
				want := std.Seal(nil, nonce, pt, ad)
				got := g.seal(nil, nonce, pt, ad)
				if !bytes.Equal(got, want) {
					t.Fatalf("key %d len %d ad %d: seal mismatch", keyLen, n, adLen)
				}
				inPlace := append([]byte(nil), pt...)
				inPlace = g.seal(inPlace[:0], nonce, inPlace, ad)
				if !bytes.Equal(inPlace, want) {
					t.Fatalf("key %d len %d ad %d: in place seal mismatch", keyLen, n, adLen)
				}
				back, err := g.open(inPlace[:0], nonce, inPlace, ad)
				if err != nil || !bytes.Equal(back, pt) {
					t.Fatalf("key %d len %d ad %d: open failed: %v", keyLen, n, adLen, err)
				}
				want[len(want)-1] ^= 1
				if _, err := g.open(nil, nonce, want, ad); err == nil {
					t.Fatalf("key %d len %d ad %d: tampered tag accepted", keyLen, n, adLen)
				}
			}
		}
	}
}
