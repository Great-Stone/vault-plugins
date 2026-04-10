package plugin

import (
	"errors"
	"strings"
)

const defaultRPID = "localhost"

// Default origins for the bundled macOS helper (Safari on port 8765).
var defaultLocalhostOrigins = []string{
	"http://localhost:8765",
	"http://127.0.0.1:8765",
}

var errOriginsRequired = errors.New("allowed_origins is required when rp_id is not localhost (or omit rp_id to use localhost defaults)")

func cleanOriginList(origins []string) []string {
	if len(origins) == 0 {
		return nil
	}
	var out []string
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o != "" {
			out = append(out, o)
		}
	}
	return out
}

func isLocalhostRPID(rpID string) bool {
	switch strings.ToLower(strings.TrimSpace(rpID)) {
	case "localhost", "127.0.0.1":
		return true
	default:
		return false
	}
}

// effectivePasskeyConfig returns a copy of cfg with defaults applied. Empty rp_id
// becomes localhost; empty allowed_origins is filled only when the effective rp_id
// is localhost or 127.0.0.1 (local helper workflow).
func effectivePasskeyConfig(cfg *configEntry) (*configEntry, error) {
	var out configEntry
	if cfg != nil {
		out = *cfg
	}
	out.RPID = strings.TrimSpace(out.RPID)
	if out.RPID == "" {
		out.RPID = defaultRPID
	}
	out.AllowedOrigins = cleanOriginList(out.AllowedOrigins)
	if len(out.AllowedOrigins) == 0 {
		if isLocalhostRPID(out.RPID) {
			out.AllowedOrigins = append([]string(nil), defaultLocalhostOrigins...)
		} else {
			return nil, errOriginsRequired
		}
	}
	return &out, nil
}
