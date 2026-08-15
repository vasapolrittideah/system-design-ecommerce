package auth

import (
	"crypto/ecdsa"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// pemPrefix is the opening line every PEM block shares.
const pemPrefix = "-----BEGIN"

// decodeKey normalises the two ways a PEM key arrives in an environment
// variable, because 12-factor config and PEM disagree about newlines.
//
// A Kubernetes Secret carries a multi-line PEM block fine, so one is accepted
// verbatim; base64 of the whole block is accepted too, being the shape that
// survives any delivery that keeps a value on one line. Which one arrived is
// decided by looking at it, because a flag is one more thing to set wrong.
func decodeKey(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, fmt.Errorf("auth: key is empty")
	}

	if strings.HasPrefix(encoded, pemPrefix) {
		return []byte(encoded), nil
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("auth: key is neither PEM nor base64-encoded PEM: %w", err)
	}

	if !strings.HasPrefix(strings.TrimSpace(string(decoded)), pemPrefix) {
		return nil, fmt.Errorf("auth: decoded key is not a PEM block")
	}

	return decoded, nil
}

func parsePrivateKey(encoded string) (*ecdsa.PrivateKey, error) {
	pem, err := decodeKey(encoded)
	if err != nil {
		return nil, err
	}

	key, err := jwt.ParseECPrivateKeyFromPEM(pem)
	if err != nil {
		return nil, fmt.Errorf("auth: parse EC private key: %w", err)
	}

	return key, nil
}

func parsePublicKey(encoded string) (*ecdsa.PublicKey, error) {
	pem, err := decodeKey(encoded)
	if err != nil {
		return nil, err
	}

	key, err := jwt.ParseECPublicKeyFromPEM(pem)
	if err != nil {
		return nil, fmt.Errorf("auth: parse EC public key: %w", err)
	}

	return key, nil
}
