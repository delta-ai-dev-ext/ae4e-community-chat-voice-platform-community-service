package handler

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"community_service/internal/models"
)

// TokenVerifier verifies identity access tokens locally against identity's
// public verification keys, so no call to identity_service sits on the request
// path.
type TokenVerifier struct {
	keys     map[string]any
	issuer   string
	audience string
	now      func() time.Time
}

// identityClaims is the subset of identity's access-token claims this service
// consumes.
type identityClaims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
}

// jwksDocument is the standard JSON Web Key Set identity publishes.
type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// NewTokenVerifier builds a verifier from the inline JWKS or the JWKS file; the
// file wins when both are configured.
func NewTokenVerifier(issuer, audience, jwksInline, jwksPath string) (*TokenVerifier, error) {
	document := []byte(jwksInline)
	if jwksPath != "" {
		content, err := os.ReadFile(jwksPath)
		if err != nil {
			return nil, fmt.Errorf("handler: read identity jwks file: %w", err)
		}
		document = content
	}
	if len(document) == 0 {
		return nil, errors.New("handler: identity jwks is empty")
	}

	keys, err := parseJWKS(document)
	if err != nil {
		return nil, err
	}

	return &TokenVerifier{
		keys:     keys,
		issuer:   issuer,
		audience: audience,
		now:      time.Now,
	}, nil
}

// parseJWKS converts a JSON Web Key Set into verification keys.
func parseJWKS(document []byte) (map[string]any, error) {
	var parsed jwksDocument
	if err := json.Unmarshal(document, &parsed); err != nil {
		return nil, fmt.Errorf("handler: decode identity jwks: %w", err)
	}
	if len(parsed.Keys) == 0 {
		return nil, errors.New("handler: identity jwks contains no keys")
	}

	keys := make(map[string]any, len(parsed.Keys))
	for index, key := range parsed.Keys {
		identifier := key.Kid
		if identifier == "" {
			identifier = fmt.Sprintf("key-%d", index)
		}

		publicKey, err := key.publicKey()
		if err != nil {
			return nil, fmt.Errorf("handler: identity jwk %q: %w", identifier, err)
		}
		keys[identifier] = publicKey
	}
	return keys, nil
}

func (k jwkKey) publicKey() (any, error) {
	switch k.Kty {
	case "RSA":
		modulus, err := base64URLDecode(k.N)
		if err != nil {
			return nil, err
		}
		exponent, err := base64URLDecode(k.E)
		if err != nil {
			return nil, err
		}
		return &rsa.PublicKey{
			N: new(big.Int).SetBytes(modulus),
			E: int(new(big.Int).SetBytes(exponent).Int64()),
		}, nil
	case "EC":
		curve, err := curveByName(k.Crv)
		if err != nil {
			return nil, err
		}
		x, err := base64URLDecode(k.X)
		if err != nil {
			return nil, err
		}
		y, err := base64URLDecode(k.Y)
		if err != nil {
			return nil, err
		}
		return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
	default:
		return nil, fmt.Errorf("unsupported key type %q", k.Kty)
	}
}

func curveByName(name string) (elliptic.Curve, error) {
	switch name {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("unsupported curve %q", name)
	}
}

func base64URLDecode(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("decode base64url value: %w", err)
	}
	return decoded, nil
}

// Identity is the authenticated caller of a public request.
type Identity struct {
	UserID    uuid.UUID
	SessionID string
}

// Verify parses and validates a bearer token, returning the caller identity.
func (v *TokenVerifier) Verify(raw string) (*Identity, error) {
	claims := &identityClaims{}

	token, err := jwt.ParseWithClaims(raw, claims, v.keyFunc,
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(v.now),
	)
	if err != nil {
		return nil, models.Wrap(models.ErrUnauthorized, err)
	}
	if !token.Valid {
		return nil, models.ErrUnauthorized
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, models.Wrap(models.ErrUnauthorized, fmt.Errorf("subject is not a user id: %w", err))
	}

	return &Identity{UserID: userID, SessionID: claims.SessionID}, nil
}

func (v *TokenVerifier) keyFunc(token *jwt.Token) (any, error) {
	if kid, ok := token.Header["kid"].(string); ok && kid != "" {
		key, found := v.keys[kid]
		if !found {
			return nil, fmt.Errorf("unknown key id %q", kid)
		}
		return key, nil
	}

	if len(v.keys) == 1 {
		for _, key := range v.keys {
			return key, nil
		}
	}
	return nil, errors.New("token has no key id and several verification keys are configured")
}
