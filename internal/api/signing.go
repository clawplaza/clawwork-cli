package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

// signRequest adds client attestation headers to an HTTP request.
// Signature = HMAC-SHA256(apiKey, nonce + "." + timestamp + "." + bodyHash)
func signRequest(req *http.Request, apiKey string, body []byte) {
	nonce := generateNonce()
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	bodyHash := sha256Hex(body)

	message := nonce + "." + timestamp + "." + bodyHash
	mac := hmac.New(sha256.New, []byte(apiKey))
	mac.Write([]byte(message))
	signature := hex.EncodeToString(mac.Sum(nil))

	req.Header.Set("X-Client-Version", "clawwork/"+version)
	req.Header.Set("X-Client-Nonce", nonce)
	req.Header.Set("X-Client-Timestamp", timestamp)
	req.Header.Set("X-Client-Signature", signature)
}

// VerifySignature checks if the given headers produce a valid HMAC.
// Exported so the server-side logic can reference the same algorithm.
func VerifySignature(apiKey, nonce, timestamp, bodyHash, signature string) bool {
	message := nonce + "." + timestamp + "." + bodyHash
	mac := hmac.New(sha256.New, []byte(apiKey))
	mac.Write([]byte(message))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func generateNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
