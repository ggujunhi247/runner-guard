// rgsign signs a release's checksums.txt with the release key, kept off
// GitHub. Hosts verify the signature before installing anything
// (internal/update.PublicKey), so a stolen GitHub account or a hijacked CI
// step alone cannot ship code to them. Maintainer tool; not released.
//
//	rgsign -genkey -key <private.pem>   make the key pair once; prints the public key
//	rgsign -pub -key <private.pem>      print the public key again
//	rgsign -key <private.pem> <file>    write <file>.sig
//	rgsign -verify <file>               check <file>.sig against the embedded public key
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/ggujunhi247/runner-guard/internal/update"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "rgsign:", err)
		os.Exit(1)
	}
}

func run() error {
	keyPath := flag.String("key", "", "private key (PEM, PKCS#8)")
	genkey := flag.Bool("genkey", false, "create a new RSA-3072 key at -key")
	pub := flag.Bool("pub", false, "print the public key of -key")
	verify := flag.Bool("verify", false, "verify <file>.sig with the embedded public key")
	flag.Parse()

	switch {
	case *verify:
		if flag.NArg() != 1 {
			return errors.New("usage: rgsign -verify <file>")
		}
		data, err := os.ReadFile(flag.Arg(0))
		if err != nil {
			return err
		}
		sig, err := os.ReadFile(flag.Arg(0) + ".sig")
		if err != nil {
			return err
		}
		if err := update.VerifySignature(data, sig); err != nil {
			return err
		}
		fmt.Println("signature ok")
		return nil
	case *genkey:
		if _, err := os.Stat(*keyPath); err == nil {
			return fmt.Errorf("%s exists; not overwriting a release key", *keyPath)
		}
		k, err := rsa.GenerateKey(rand.Reader, 3072)
		if err != nil {
			return err
		}
		der, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			return err
		}
		if err := os.WriteFile(*keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
			return err
		}
		return printPub(&k.PublicKey)
	}

	k, err := loadKey(*keyPath)
	if err != nil {
		return err
	}
	if *pub {
		return printPub(&k.PublicKey)
	}
	if flag.NArg() != 1 {
		return errors.New("usage: rgsign -key <private.pem> <file>")
	}
	data, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		return err
	}
	h := sha256.Sum256(data)
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, h[:])
	if err != nil {
		return err
	}
	// Refuse to write a signature the shipped binaries would reject.
	if err := update.VerifySignature(data, sig); err != nil {
		return fmt.Errorf("this key is not the one in internal/update.PublicKey: %w", err)
	}
	return os.WriteFile(flag.Arg(0)+".sig", sig, 0o644)
}

func loadKey(path string) (*rsa.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, fmt.Errorf("%s: no PEM block", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an RSA key", path)
	}
	return rk, nil
}

// printPub prints the public key in the three forms the verifiers embed.
func printPub(k *rsa.PublicKey) error {
	der, err := x509.MarshalPKIXPublicKey(k)
	if err != nil {
		return err
	}
	fmt.Print(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})))
	fmt.Println("modulus (base64, install.ps1):", base64.StdEncoding.EncodeToString(k.N.Bytes()))
	fmt.Println("exponent (base64):", base64.StdEncoding.EncodeToString([]byte{byte(k.E >> 16), byte(k.E >> 8), byte(k.E)}))
	return nil
}
