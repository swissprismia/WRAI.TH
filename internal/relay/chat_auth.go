package relay

import (
	"encoding/base64"
	"encoding/json"
)

// ClientPrincipal holds the parsed EasyAuth identity.
type ClientPrincipal struct {
	Email  string // email extracted from claims; "dev@local" when dev-mode fallback
	Name   string // display name extracted from claims; empty when not present
	Source string // "easyauth" or "dev"
}

// email claim type URNs recognised by Azure App Service EasyAuth.
var emailClaimTypes = map[string]bool{
	"preferred_username": true,
	"emails":             true,
	"email":              true,
	"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress": true,
	"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/upn":          true,
}

// name claim type URNs recognised by Azure App Service EasyAuth.
var nameClaimTypes = map[string]bool{
	"name": true,
	"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name": true,
}

type easyAuthClaim struct {
	Typ string `json:"typ"`
	Val string `json:"val"`
}

type easyAuthPrincipal struct {
	Claims []easyAuthClaim `json:"claims"`
}

// parseClientPrincipal decodes the X-MS-CLIENT-PRINCIPAL header (base64 JSON)
// and extracts the user's email address and display name. Returns nil if the
// header is absent or unparseable. Accepts both standard and URL-safe base64.
func parseClientPrincipal(header string) *ClientPrincipal {
	if header == "" {
		return nil
	}

	decoded, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(header)
		if err != nil {
			return nil
		}
	}

	var raw easyAuthPrincipal
	if err := json.Unmarshal(decoded, &raw); err != nil {
		return nil
	}

	var email, name string
	for _, c := range raw.Claims {
		if email == "" && emailClaimTypes[c.Typ] && c.Val != "" {
			email = c.Val
		}
		if name == "" && nameClaimTypes[c.Typ] && c.Val != "" {
			name = c.Val
		}
	}

	if email == "" {
		return nil
	}

	return &ClientPrincipal{Email: email, Name: name, Source: "easyauth"}
}
