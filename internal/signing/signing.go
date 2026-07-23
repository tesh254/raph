package signing

import (
	_ "embed"
	"fmt"

	"aead.dev/minisign"
)

const (
	DefaultPrivateKeyEnv = "RAPH_MINISIGN_PRIVATE_KEY"
	DefaultPasswordEnv   = "RAPH_MINISIGN_PASSWORD"
)

//go:embed raph.minisign.pub
var trustedPublicKeyText []byte

func TrustedPublicKeyText() []byte {
	return append([]byte(nil), trustedPublicKeyText...)
}

func TrustedPublicKey() (minisign.PublicKey, error) {
	return ParsePublicKey(trustedPublicKeyText)
}

func ParsePublicKey(text []byte) (minisign.PublicKey, error) {
	var key minisign.PublicKey
	if err := key.UnmarshalText(text); err != nil {
		return minisign.PublicKey{}, fmt.Errorf("parse minisign public key: %w", err)
	}
	return key, nil
}

func ParsePrivateKey(text []byte, password string) (minisign.PrivateKey, error) {
	key, err := minisign.DecryptKey(password, text)
	if err != nil {
		return minisign.PrivateKey{}, fmt.Errorf("decrypt minisign private key: %w", err)
	}
	return key, nil
}

// DerivePublicKeyText returns the minisign public key that matches a signing
// private key, in the text form used by raph.minisign.pub. It's the source of
// truth for the embedded verification key: if that file drifts from the key the
// release pipeline signs with, `raph update` fails signature verification.
func DerivePublicKeyText(privateKeyText []byte, password string) ([]byte, error) {
	privateKey, err := ParsePrivateKey(privateKeyText, password)
	if err != nil {
		return nil, err
	}
	publicKey, ok := privateKey.Public().(minisign.PublicKey)
	if !ok {
		return nil, fmt.Errorf("unexpected public key type %T", privateKey.Public())
	}
	return publicKey.MarshalText()
}

func SignMessage(privateKey minisign.PrivateKey, message []byte, trustedComment string) ([]byte, error) {
	return minisign.SignWithComments(privateKey, message, trustedComment, "signature from raph release pipeline"), nil
}

func VerifyMessage(publicKey minisign.PublicKey, message []byte, signature []byte) error {
	if !minisign.Verify(publicKey, message, signature) {
		return fmt.Errorf("minisign verification failed")
	}
	return nil
}

func VerifyTrustedMessage(message []byte, signature []byte) error {
	publicKey, err := TrustedPublicKey()
	if err != nil {
		return err
	}
	return VerifyMessage(publicKey, message, signature)
}
