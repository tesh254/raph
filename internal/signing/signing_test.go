package signing

import (
	"bytes"
	"crypto/rand"
	"testing"

	"aead.dev/minisign"
)

func TestTrustedPublicKeyParses(t *testing.T) {
	key, err := TrustedPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if key.ID() == 0 {
		t.Fatal("expected non-zero key id")
	}
}

func TestSignAndVerifyMessage(t *testing.T) {
	publicKey, privateKey, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	message := []byte("raph release integrity")
	signature, err := SignMessage(privateKey, message, "test signature")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyMessage(publicKey, message, signature); err != nil {
		t.Fatal(err)
	}
	if err := VerifyMessage(publicKey, append([]byte(nil), message...), signature); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyMessageRejectsTampering(t *testing.T) {
	publicKey, privateKey, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	message := []byte("raph release integrity")
	signature, err := SignMessage(privateKey, message, "test signature")
	if err != nil {
		t.Fatal(err)
	}

	tampered := bytes.ReplaceAll(message, []byte("integrity"), []byte("tampering"))
	if err := VerifyMessage(publicKey, tampered, signature); err == nil {
		t.Fatal("expected verification failure for tampered message")
	}
}
