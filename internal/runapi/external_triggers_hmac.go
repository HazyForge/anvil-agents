package runapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const githubSignaturePrefix = "sha256="

// verifyGitHubSignature256 validates X-Hub-Signature-256 against the raw body.
// It does not log the secret or signature material.
func verifyGitHubSignature256(secret, signatureHeader string, body []byte) error {
	signatureHeader = strings.TrimSpace(signatureHeader)
	if signatureHeader == "" {
		return fmt.Errorf("missing X-Hub-Signature-256")
	}
	if !strings.HasPrefix(signatureHeader, githubSignaturePrefix) {
		return fmt.Errorf("unsupported signature scheme")
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signatureHeader, githubSignaturePrefix))
	if err != nil {
		return fmt.Errorf("invalid signature encoding")
	}
	if len(secret) == 0 {
		return fmt.Errorf("webhook secret is empty")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	if !hmac.Equal(provided, expected) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}
