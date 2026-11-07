package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"community_service/internal/handler"
	"community_service/internal/models"
	"community_service/internal/obs"
	"community_service/internal/service"
	"community_service/tests/mocks"
)

const (
	testIssuer     = "https://identity.example.test"
	testAudience   = "community_service"
	testKeyID      = "identity-key-1"
	testServiceTok = "internal-service-token"
)

// testNow is the frozen clock of the integration suite.
var testNow = time.Date(2026, 2, 5, 12, 0, 0, 0, time.UTC)

var (
	keyOnce    sync.Once
	signingKey *rsa.PrivateKey
	jwksInline string
	keyErr     error
)

// identityKeys generates the identity key pair once per test binary and renders
// the JWKS document the service verifies tokens against.
func identityKeys(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()

	keyOnce.Do(func() {
		signingKey, keyErr = rsa.GenerateKey(rand.Reader, 2048)
		if keyErr != nil {
			return
		}

		publicKey := signingKey.PublicKey
		document := map[string]any{
			"keys": []map[string]string{{
				"kid": testKeyID,
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(publicKey.E)).Bytes()),
			}},
		}

		encoded, err := json.Marshal(document)
		if err != nil {
			keyErr = err
			return
		}
		jwksInline = string(encoded)
	})

	require.NoError(t, keyErr)
	return signingKey, jwksInline
}

// stubReadiness is the injectable readiness check of the probe tests.
type stubReadiness struct {
	err error
}

// Ready returns the injected readiness error.
func (s *stubReadiness) Ready(context.Context) error { return s.err }

// rig bundles the router under test with the doubles it was built from.
type rig struct {
	engine    *gin.Engine
	repo      *mocks.Repository
	publisher *mocks.Publisher
	clock     *mocks.Clock
	readiness *stubReadiness
	metrics   *obs.Metrics
	key       *rsa.PrivateKey
	userID    uuid.UUID
	token     string
}

// newRig wires the real router over mocked persistence and a capturing producer.
func newRig(t *testing.T) *rig {
	t.Helper()

	key, jwks := identityKeys(t)
	userID := uuid.New()

	repository := &mocks.Repository{}
	publisher := mocks.NewPublisher()
	clock := mocks.NewClock(testNow)
	metrics := obs.NewMetrics()
	readiness := &stubReadiness{}

	deps := service.Deps{
		Communities: repository,
		Channels:    repository,
		Invites:     repository,
		Memberships: repository,
		Roles:       repository,
		Sanctions:   repository,
		Publisher:   publisher,
		Clock:       clock,
		Logger:      zap.NewNop(),
		Metrics:     metrics,
	}

	tokens, err := handler.NewTokenVerifier(testIssuer, testAudience, jwks, "")
	require.NoError(t, err)

	h := &handler.Handler{
		Communities:   service.NewCommunityService(deps),
		Channels:      service.NewChannelService(deps),
		Invites:       service.NewInviteService(deps),
		Memberships:   service.NewMembershipService(deps),
		Roles:         service.NewRoleService(deps),
		Authorization: service.NewAuthorizationService(deps),
		Tokens:        tokens,
		InternalToken: testServiceTok,
		Readiness:     readiness,
		Logger:        zap.NewNop(),
		Metrics:       metrics,
	}

	engine := h.NewRouter(&handler.Middleware{
		Tokens:        tokens,
		InternalToken: testServiceTok,
		Logger:        zap.NewNop(),
		Metrics:       metrics,
	})

	return &rig{
		engine:    engine,
		repo:      repository,
		publisher: publisher,
		clock:     clock,
		readiness: readiness,
		metrics:   metrics,
		key:       key,
		userID:    userID,
		token:     mintToken(t, key, userID),
	}
}

// mintToken signs an identity access token for the given user.
func mintToken(t *testing.T, key *rsa.PrivateKey, userID uuid.UUID) string {
	t.Helper()

	claims := jwt.MapClaims{
		"sub": userID.String(),
		"sid": uuid.NewString(),
		"iss": testIssuer,
		"aud": testAudience,
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = testKeyID

	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

// requestOptions mutates an outgoing test request.
type requestOptions func(*http.Request)

// withBearer overrides the Authorization header.
func withBearer(token string) requestOptions {
	return func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer "+token)
	}
}

// withoutAuthorization removes the Authorization header.
func withoutAuthorization() requestOptions {
	return func(request *http.Request) {
		request.Header.Del("Authorization")
	}
}

// withHeader sets an arbitrary header.
func withHeader(name, value string) requestOptions {
	return func(request *http.Request) {
		request.Header.Set(name, value)
	}
}

// call performs an authenticated JSON request.
func (r *rig) call(t *testing.T, method, path string, body any, options ...requestOptions) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(encoded)
	}

	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+r.token)
	for _, option := range options {
		option(request)
	}

	recorder := httptest.NewRecorder()
	r.engine.ServeHTTP(recorder, request)
	return recorder
}

// callWithoutAuth performs a request that carries no Authorization header.
func (r *rig) callWithoutAuth(t *testing.T, method, path string, body any, options ...requestOptions) *httptest.ResponseRecorder {
	t.Helper()
	return r.call(t, method, path, body, append([]requestOptions{withoutAuthorization()}, options...)...)
}

// callInternal performs a request against an internal oracle with the service
// token unless an option overrides it.
func (r *rig) callInternal(t *testing.T, path string, body any, options ...requestOptions) *httptest.ResponseRecorder {
	t.Helper()
	return r.call(t, http.MethodPost, path, body, append([]requestOptions{withBearer(testServiceTok)}, options...)...)
}

// decode unmarshals the response body.
func decode[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()

	var value T
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &value), "body: %s", recorder.Body.String())
	return value
}

// errorResponse decodes the platform error shape.
func errorResponse(t *testing.T, recorder *httptest.ResponseRecorder) models.ErrorResponse {
	t.Helper()
	return decode[models.ErrorResponse](t, recorder)
}

// seedCommunity builds a community with an owner and the two default channels.
func seedCommunity(ownerID uuid.UUID) (*models.Community, *models.Channel, *models.Channel) {
	communityID := uuid.New()
	community := &models.Community{
		ID:          communityID,
		Name:        "Go Gophers",
		OwnerUserID: ownerID,
		CreatedAt:   testNow,
		UpdatedAt:   testNow,
	}

	capacity := models.DefaultVoiceCapacity
	text := &models.Channel{
		ID: uuid.New(), CommunityID: communityID, Kind: models.ChannelKindText,
		Name: "general", Position: 0, ChannelVersion: 1, CreatedAt: testNow, UpdatedAt: testNow,
	}
	voice := &models.Channel{
		ID: uuid.New(), CommunityID: communityID, Kind: models.ChannelKindVoice,
		Name: "General", Position: 1, Capacity: &capacity, ChannelVersion: 1,
		CreatedAt: testNow, UpdatedAt: testNow,
	}
	return community, text, voice
}

// seedMembership builds the membership of a caller in a community.
func seedMembership(communityID, userID uuid.UUID, roleIDs ...uuid.UUID) *models.Membership {
	if roleIDs == nil {
		roleIDs = []uuid.UUID{}
	}
	return &models.Membership{
		ID:            uuid.New(),
		CommunityID:   communityID,
		UserID:        userID,
		Status:        models.StatusActive,
		JoinedAt:      testNow,
		MemberVersion: 1,
		RoleIDs:       roleIDs,
	}
}

// armAccess arms the reads the caller resolution performs. Extra expectations are
// harmless because no test asserts every expectation was consumed.
func (r *rig) armAccess(community *models.Community, membership *models.Membership, roles []*models.Role) {
	r.repo.On("GetCommunity", anyContext(), community.ID).Return(community, nil)
	if membership == nil {
		r.repo.On("GetMembership", anyContext(), community.ID, anyUser()).Return(nil, models.ErrMembershipNotFound)
		return
	}

	r.repo.On("GetMembership", anyContext(), community.ID, membership.UserID).Return(membership, nil)
	r.repo.On("ListRoles", anyContext(), community.ID).Return(roles, nil)
}

// armCommunityReads arms the reads beyond caller resolution that community and
// channel handlers perform.
func (r *rig) armCommunityReads(communityID uuid.UUID, channels ...*models.Channel) {
	r.repo.On("ListChannels", anyContext(), communityID).Return(channels, nil)
}

// anyContext matches any context argument of a mocked call.
func anyContext() any { return mock.Anything }

// anyUser matches any user id argument of a mocked call.
func anyUser() any { return mock.Anything }
