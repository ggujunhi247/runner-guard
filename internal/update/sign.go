package update

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

// PublicKeys verify checksums.txt.sig; a signature by any of them is accepted.
// Their private halves stay off GitHub (cmd/rgsign). The first is the one in
// use; the second is a backup kept offline, so a lost key never strands hosts
// and a leaked one can be dropped by a release signed with the other. The
// same keys are in install.sh and install.ps1 (internal/policy checks it).
// Tests swap them for their own.
var PublicKeys = []string{`-----BEGIN PUBLIC KEY-----
MIIBojANBgkqhkiG9w0BAQEFAAOCAY8AMIIBigKCAYEA4xuHI4gBNCLStJKC0Fc2
FdwhBJleuR7p9awiT8d8TXJO1zZaWowAhz8nLIwq02NBRXdC8X6MMeBgqA194Llb
poZk0AulasLIfSLpCq9GfPEjUUfIlwVOoeouROVl2VQJPZs7Sy/iffFaux5uOPJG
9cLj5XqHiy5q6UnJey7kwyOl7Bi/rblULz95dF37WTmh7Gxikkh5cmXUPig6D82c
T02YoEvKOLUK6m94KqA1HJ+wLpVrjPQA52u8dyAtr6Dl+ajlnkZH7Pq1tQB4C5RC
h1NJz75mpBupKgnTqzmrzFEcUZZC3K6pVejeZnBYZRY7j8UCzSqZgyNLBIY+9hLo
/wIqG4XYPDJ/bVS2vcRAex6YmbnxA9Ur3/14ZBayfEliuIkegDOgm0cza7RuqHK2
NxtdN4ChKEFWJgjm+Sw7ZMAUDPkTUrmQ29Pyd5HFmwRfWgF9RjEbmD6A5Jf5sgOO
cZuuc5mziBKQAeDhjaT5nShJ9VkxdIep5m3vx8Cs5bn5AgMBAAE=
-----END PUBLIC KEY-----
`, `-----BEGIN PUBLIC KEY-----
MIIBojANBgkqhkiG9w0BAQEFAAOCAY8AMIIBigKCAYEA2/gLMUkbQ3Ieml5ASxxD
ILvXKhmAsaQXc78u4fUfIkNE8xdCOZuRJLh7nR3rZU/rWDuptHy6rwORbDbYNZ4b
pNNbx0EFMLij4iAoGHzt2WZSre4lrQTq9+sOyUJgsDR33TW2eVFQb/6yQGqyKqVq
kivf+uJKge291pf+o03W3J7ZK243OQyr+biNmYs/Y/jecE6FxELRVWc203rEXdfX
8BN9l1+zEMKpTXI9jy+/e5mFpHgGbg7QBrTpcBisUj41MUVl6Jl7KbyrATJ586pq
d8l4LiiicV9rqxNqFJRN6jGRJUhaD7DxUw6Cyrhsx69Rxr04DTurWDGgkuo1iHzV
x/d7lvVUgV+/Go6H9aBZNkbiUKoSaxy9a1YTHtjWw9JJBr13mg3ILm8sWQC5jn+O
4ggstjcYV1t0yv5gDBbQWPdDLahS2USq9I9Bu9AbaPdOCkuGC/H+mCWdC1hKVIAW
dJCnFgKls3ZOHtUeaS7WGR2vEVDyjDF3lROcdiKboIrBAgMBAAE=
-----END PUBLIC KEY-----
`}

// VerifySignature checks an RSA PKCS#1 v1.5 SHA-256 signature over data.
func VerifySignature(data, sig []byte) error {
	if len(PublicKeys) == 0 {
		return errors.New("no release public key built in")
	}
	h := sha256.Sum256(data)
	for i, p := range PublicKeys {
		blk, _ := pem.Decode([]byte(p))
		if blk == nil {
			return fmt.Errorf("release public key %d: no PEM", i)
		}
		k, err := x509.ParsePKIXPublicKey(blk.Bytes)
		if err != nil {
			return fmt.Errorf("release public key %d: %w", i, err)
		}
		rk, ok := k.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("release public key %d is not RSA", i)
		}
		if rsa.VerifyPKCS1v15(rk, crypto.SHA256, h[:], sig) == nil {
			return nil
		}
	}
	return errors.New("checksums.txt signature does not match any release key")
}
