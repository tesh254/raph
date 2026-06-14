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
