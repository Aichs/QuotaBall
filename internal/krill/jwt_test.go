package krill

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestJWTExpiredUsesPayloadExp(t *testing.T) {
	expired := makeToken(`{"exp":10}`)
	future := makeToken(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(time.Hour).Unix()))

	if !JWTExpired(expired, time.Unix(20, 0)) {
		t.Fatal("expired token was accepted")
	}
	if JWTExpired(future, time.Now()) {
		t.Fatal("future token was treated as expired")
	}
	if !JWTExpired("not-a-jwt", time.Now()) {
		t.Fatal("malformed token should be treated as expired")
	}
}

func makeToken(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return strings.Join([]string{"header", enc, "sig"}, ".")
}
