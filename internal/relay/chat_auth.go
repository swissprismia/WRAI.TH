package relay

import (
	"encoding/base64"
	"encoding/json"
)

// ClientPrincipal holds the parsed EasyAuth identity.
type ClientPrincipal struct {
	Email  string // email extracted from claims; "dev@local" when dev-mode fallback
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

type easyAuthClaim struct {
	Typ string `json:"typ"`
	Val string `json:"val"`
}

type easyAuthPrincipal struct {
	Claims []easyAuthClaim `json:"claims"`
}

// parseClientPrincipal decodes the X-MS-CLIENT-PRINCIPAL header (base64 JSON)
// and extracts the user's email address. Returns nil if the header is absent or unparseable.
func parseClientPrincipal(header string) *ClientPrincipal {
	if header == "" {
		return nil
	}

	decoded, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		return nil
	}

	var principal easyAuthPrincipal
	if err := json.Unmarshal(decoded, &principal); err != nil {
		return nil
	}

	for _, c := range principal.Claims {
		if emailClaimTypes[c.Typ] && c.Val != "" {
			return &ClientPrincipal{Email: c.Val, Source: "easyauth"}
		}
	}

	return nil
}
