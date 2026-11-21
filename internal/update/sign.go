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

// PublicKey verifies checksums.txt.sig. Its private half stays off GitHub,
// with the maintainer (cmd/rgsign), so a release must be signed by hand
// before hosts accept it. The same key is in install.sh and install.ps1.
// Tests swap it for their own.
var PublicKey = `-----BEGIN PUBLIC KEY-----
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
`

// VerifySignature checks an RSA PKCS#1 v1.5 SHA-256 signature over data.
func VerifySignature(data, sig []byte) error {
	blk, _ := pem.Decode([]byte(PublicKey))
	if blk == nil {
		return errors.New("no release public key built in")
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return fmt.Errorf("release public key: %w", err)
	}
	rk, ok := k.(*rsa.PublicKey)
	if !ok {
		return errors.New("release public key is not RSA")
	}
	h := sha256.Sum256(data)
	if err := rsa.VerifyPKCS1v15(rk, crypto.SHA256, h[:], sig); err != nil {
		return errors.New("checksums.txt signature does not match the release key")
	}
	return nil
}
