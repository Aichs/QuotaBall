package krill

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

func JWTExpired(token string, now time.Time) bool {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return true
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return true
		}
	}

	var body struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &body); err != nil || body.Exp <= 0 {
		return true
	}
	return now.Unix() > body.Exp
}
