package audit

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
)

const (
	AES256GCMAlgorithm        = "aes-256-gcm"
	AESGCMProtectionProfileV1 = "frostgrove.audit.protection.v1"
)

type ProtectionRequest struct{ value protectionRequest }
type protectionRequest struct {
	plaintext []byte
	aad       []byte
}

type RevealRequest struct{ value revealRequest }
type revealRequest struct {
	envelope ProtectedValue
	aad      []byte
}

type TokenizeRequest struct{ value tokenizeRequest }
type tokenizeRequest struct {
	plaintext   []byte
	aad         []byte
	description TokenDescription
}

type TokenQuery struct{ value tokenQuery }
type tokenQuery struct {
	requests []TokenizeRequest
	origin   *struct{}
}

type TokenQueryResult struct{ value tokenQueryResult }
type tokenQueryResult struct {
	tokens []Token
}

type Protector interface {
	Description() ProtectionDescription
	Protect(context.Context, ProtectionRequest) (ProtectedValue, error)
}

type Revealer interface {
	Descriptions() []ProtectionDescription
	Reveal(context.Context, RevealRequest) ([]byte, error)
}

type Tokenizer interface {
	ActiveDescription() TokenDescription
	Descriptions() []TokenDescription
	Tokenize(context.Context, TokenizeRequest) (Token, error)
	QueryTokens(context.Context, TokenQuery) (TokenQueryResult, error)
}

type Signer interface {
	Description() SignatureDescription
	Sign(context.Context, IntegrityDigest) (Seal, error)
}

type Verifier interface {
	Descriptions() []SignatureDescription
	Verify(context.Context, IntegrityDigest, Seal) error
}

func (r ProtectionRequest) Plaintext() []byte    { return bytes.Clone(r.value.plaintext) }
func (r ProtectionRequest) AAD() []byte          { return bytes.Clone(r.value.aad) }
func (r RevealRequest) Envelope() ProtectedValue { return r.value.envelope }
func (r RevealRequest) AAD() []byte              { return bytes.Clone(r.value.aad) }
func (r TokenizeRequest) Plaintext() []byte      { return bytes.Clone(r.value.plaintext) }
func (r TokenizeRequest) AAD() []byte            { return bytes.Clone(r.value.aad) }
func (r TokenizeRequest) Description() TokenDescription {
	return r.value.description
}
func (q TokenQuery) Requests() []TokenizeRequest { return slices.Clone(q.value.requests) }
func (r TokenQueryResult) Tokens() []Token       { return slices.Clone(r.value.tokens) }

func NewTokenQueryResult(query TokenQuery, tokens []Token) (TokenQueryResult, error) {
	if query.value.origin == nil || len(tokens) != len(query.value.requests) {
		return TokenQueryResult{}, auditErrorAt(ErrInvalid, "token_query")
	}
	for index, token := range tokens {
		request := query.value.requests[index]
		if token.Algorithm() != request.value.description.Algorithm || token.Profile() != request.value.description.Profile || token.KeyID() != request.value.description.KeyID || len(token.Bytes()) == 0 {
			return TokenQueryResult{}, auditErrorAt(ErrInvalid, "tokens")
		}
	}
	return TokenQueryResult{value: tokenQueryResult{tokens: slices.Clone(tokens)}}, nil
}

func newProtectionRequest(plaintext, aad []byte) (ProtectionRequest, error) {
	if len(plaintext) == 0 || len(plaintext) > MaxValueBytes || len(aad) == 0 || len(aad) > MaxReferenceBytes {
		return ProtectionRequest{}, auditErrorAt(ErrInvalid, "protection")
	}
	return ProtectionRequest{value: protectionRequest{plaintext: bytes.Clone(plaintext), aad: bytes.Clone(aad)}}, nil
}

func newRevealRequest(envelope ProtectedValue, aad []byte) (RevealRequest, error) {
	if envelope.Algorithm() == "" || len(aad) == 0 || len(aad) > MaxReferenceBytes {
		return RevealRequest{}, auditErrorAt(ErrInvalid, "reveal")
	}
	return RevealRequest{value: revealRequest{envelope: envelope, aad: bytes.Clone(aad)}}, nil
}

func newTokenizeRequest(plaintext, aad []byte, description TokenDescription) (TokenizeRequest, error) {
	if len(plaintext) == 0 || len(plaintext) > MaxValueBytes || len(aad) == 0 || len(aad) > MaxReferenceBytes {
		return TokenizeRequest{}, auditErrorAt(ErrInvalid, "token")
	}
	if err := validateProviderDescription(description.Algorithm, description.Profile, description.KeyID); err != nil {
		return TokenizeRequest{}, err
	}
	return TokenizeRequest{value: tokenizeRequest{plaintext: bytes.Clone(plaintext), aad: bytes.Clone(aad), description: description}}, nil
}

type AESGCMKey struct {
	KeyID  string
	Key    []byte
	Active bool
}

type aesGCMKey struct {
	description ProtectionDescription
	key         []byte
	active      bool
}

type aesGCMProtection struct {
	keys   []aesGCMKey
	active int
}

type ProtectionKeyring interface {
	Protector
	Revealer
}

func AESGCMProtection(keys ...AESGCMKey) (ProtectionKeyring, error) {
	if len(keys) == 0 || len(keys) > MaxCatalogs {
		return nil, auditErrorAt(ErrDeclaration, "protection.keys")
	}
	values := make([]aesGCMKey, len(keys))
	seen := make(map[string]struct{}, len(keys))
	activeKey := ""
	for index, key := range keys {
		if !validProviderLabel(key.KeyID) || len(key.Key) != 32 {
			return nil, auditErrorAt(ErrDeclaration, "protection.key")
		}
		if _, duplicate := seen[key.KeyID]; duplicate {
			return nil, auditErrorAt(ErrDeclaration, "protection.key")
		}
		seen[key.KeyID] = struct{}{}
		values[index] = aesGCMKey{
			description: ProtectionDescription{Algorithm: AES256GCMAlgorithm, Profile: AESGCMProtectionProfileV1, KeyID: key.KeyID},
			key:         bytes.Clone(key.Key), active: key.Active,
		}
		if key.Active {
			if activeKey != "" {
				return nil, auditErrorAt(ErrDeclaration, "protection.active")
			}
			activeKey = key.KeyID
		}
	}
	if activeKey == "" {
		return nil, auditErrorAt(ErrDeclaration, "protection.active")
	}
	slices.SortFunc(values, func(left, right aesGCMKey) int {
		return strings.Compare(left.description.KeyID, right.description.KeyID)
	})
	active := 0
	for index := range values {
		if values[index].description.KeyID == activeKey {
			active = index
		}
	}
	return &aesGCMProtection{keys: values, active: active}, nil
}

func (p *aesGCMProtection) Description() ProtectionDescription {
	if p == nil || len(p.keys) == 0 || p.active < 0 || p.active >= len(p.keys) {
		return ProtectionDescription{}
	}
	return p.keys[p.active].description
}

func (p *aesGCMProtection) Descriptions() []ProtectionDescription {
	if p == nil {
		return nil
	}
	output := make([]ProtectionDescription, len(p.keys))
	for index, key := range p.keys {
		output[index] = key.description
	}
	return output
}

func (p *aesGCMProtection) Protect(ctx context.Context, request ProtectionRequest) (ProtectedValue, error) {
	if p == nil || len(request.value.plaintext) == 0 || len(request.value.aad) == 0 {
		return ProtectedValue{}, auditErrorAt(ErrInvalid, "protection")
	}
	if err := ctx.Err(); err != nil {
		return ProtectedValue{}, err
	}
	key := p.keys[p.active]
	block, err := aes.NewCipher(key.key)
	if err != nil {
		return ProtectedValue{}, CryptoFailure(CryptoBackend, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return ProtectedValue{}, CryptoFailure(CryptoBackend, err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ProtectedValue{}, CryptoFailure(CryptoBackend, err)
	}
	ciphertext := aead.Seal(nil, nonce, request.value.plaintext, request.value.aad)
	return NewProtectedValue(key.description.Algorithm, key.description.Profile, key.description.KeyID, nonce, ciphertext)
}

func (p *aesGCMProtection) Reveal(ctx context.Context, request RevealRequest) ([]byte, error) {
	if p == nil || len(request.value.aad) == 0 {
		return nil, auditErrorAt(ErrInvalid, "reveal")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var key []byte
	for _, candidate := range p.keys {
		if candidate.description.Algorithm == request.value.envelope.Algorithm() && candidate.description.Profile == request.value.envelope.Profile() && candidate.description.KeyID == request.value.envelope.KeyID() {
			key = candidate.key
			break
		}
	}
	if key == nil {
		return nil, CryptoFailure(CryptoMissingKey, ErrMissingKey)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, CryptoFailure(CryptoBackend, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, CryptoFailure(CryptoBackend, err)
	}
	plaintext, err := aead.Open(nil, request.value.envelope.Nonce(), request.value.envelope.Ciphertext(), request.value.aad)
	if err != nil {
		return nil, CryptoFailure(CryptoMalformed, err)
	}
	return plaintext, nil
}

type HMACTokenKey struct {
	KeyID  string
	Key    []byte
	Active bool
}

type hmacTokenKey struct {
	description TokenDescription
	key         []byte
	active      bool
}

type hmacTokenizer struct {
	keys   []hmacTokenKey
	active int
}

func HMACTokenizer(keys ...HMACTokenKey) (Tokenizer, error) {
	if len(keys) == 0 || len(keys) > MaxCatalogs {
		return nil, auditErrorAt(ErrDeclaration, "token.keys")
	}
	values := make([]hmacTokenKey, len(keys))
	activeKey := ""
	seen := map[string]struct{}{}
	for index, key := range keys {
		if !validProviderLabel(key.KeyID) || len(key.Key) < sha256.Size {
			return nil, auditErrorAt(ErrDeclaration, "token.key")
		}
		if _, duplicate := seen[key.KeyID]; duplicate {
			return nil, auditErrorAt(ErrDeclaration, "token.key")
		}
		seen[key.KeyID] = struct{}{}
		values[index] = hmacTokenKey{description: TokenDescription{Algorithm: HMACSHA256Algorithm, Profile: HMACTokenProfileV1, KeyID: key.KeyID}, key: bytes.Clone(key.Key), active: key.Active}
		if key.Active {
			if activeKey != "" {
				return nil, auditErrorAt(ErrDeclaration, "token.active")
			}
			activeKey = key.KeyID
		}
	}
	if activeKey == "" {
		return nil, auditErrorAt(ErrDeclaration, "token.active")
	}
	slices.SortFunc(values, func(left, right hmacTokenKey) int {
		return strings.Compare(left.description.KeyID, right.description.KeyID)
	})
	active := 0
	for index := range values {
		if values[index].description.KeyID == activeKey {
			active = index
		}
	}
	return &hmacTokenizer{keys: values, active: active}, nil
}

func (t *hmacTokenizer) ActiveDescription() TokenDescription {
	if t == nil || len(t.keys) == 0 || t.active < 0 || t.active >= len(t.keys) {
		return TokenDescription{}
	}
	return t.keys[t.active].description
}

func (t *hmacTokenizer) Descriptions() []TokenDescription {
	if t == nil {
		return nil
	}
	output := make([]TokenDescription, len(t.keys))
	for index, key := range t.keys {
		output[index] = key.description
	}
	return output
}

func (t *hmacTokenizer) Tokenize(ctx context.Context, request TokenizeRequest) (Token, error) {
	if t == nil || len(request.value.plaintext) == 0 || len(request.value.aad) == 0 {
		return Token{}, auditErrorAt(ErrInvalid, "token")
	}
	if err := ctx.Err(); err != nil {
		return Token{}, err
	}
	for _, key := range t.keys {
		if key.description != request.value.description {
			continue
		}
		mac := hmac.New(sha256.New, key.key)
		writeFrame(mac, []byte(HMACTokenProfileV1))
		writeFrame(mac, request.value.aad)
		writeFrame(mac, request.value.plaintext)
		return NewToken(key.description.Algorithm, key.description.Profile, key.description.KeyID, mac.Sum(nil))
	}
	return Token{}, CryptoFailure(CryptoMissingKey, ErrMissingKey)
}

func (t *hmacTokenizer) QueryTokens(ctx context.Context, query TokenQuery) (TokenQueryResult, error) {
	if query.value.origin == nil {
		return TokenQueryResult{}, auditErrorAt(ErrInvalid, "token_query")
	}
	output := make([]Token, len(query.value.requests))
	for index, request := range query.value.requests {
		token, err := t.Tokenize(ctx, request)
		if err != nil {
			return TokenQueryResult{}, err
		}
		output[index] = token
	}
	return NewTokenQueryResult(query, output)
}

type HMACSigningKey struct {
	KeyID string
	Key   []byte
}

type hmacSigner struct {
	description SignatureDescription
	key         []byte
}

func HMACSigner(key HMACSigningKey) (Signer, error) {
	if !validProviderLabel(key.KeyID) || len(key.Key) < sha256.Size {
		return nil, auditErrorAt(ErrDeclaration, "signature.key")
	}
	return &hmacSigner{description: SignatureDescription{Algorithm: HMACSHA256Algorithm, Profile: HMACSignatureProfileV1, KeyID: key.KeyID}, key: bytes.Clone(key.Key)}, nil
}

func (s *hmacSigner) Description() SignatureDescription {
	if s == nil {
		return SignatureDescription{}
	}
	return s.description
}

func (s *hmacSigner) Sign(ctx context.Context, digest IntegrityDigest) (Seal, error) {
	if s == nil || digest == (IntegrityDigest{}) {
		return Seal{}, auditErrorAt(ErrInvalid, "signature")
	}
	if err := ctx.Err(); err != nil {
		return Seal{}, err
	}
	mac := hmac.New(sha256.New, s.key)
	writeFrame(mac, []byte(HMACSignatureProfileV1))
	writeFrame(mac, digest[:])
	return NewSeal(s.description.Algorithm, s.description.Profile, s.description.KeyID, mac.Sum(nil))
}

type HMACVerificationKey struct {
	KeyID string
	Key   []byte
}

type hmacVerifier struct {
	keys map[string][]byte
}

func HMACVerifier(keys ...HMACVerificationKey) (Verifier, error) {
	if len(keys) == 0 || len(keys) > MaxCatalogs {
		return nil, auditErrorAt(ErrDeclaration, "verification.keys")
	}
	values := make(map[string][]byte, len(keys))
	for _, key := range keys {
		if !validProviderLabel(key.KeyID) || len(key.Key) < sha256.Size {
			return nil, auditErrorAt(ErrDeclaration, "verification.key")
		}
		if _, duplicate := values[key.KeyID]; duplicate {
			return nil, auditErrorAt(ErrDeclaration, "verification.key")
		}
		values[key.KeyID] = bytes.Clone(key.Key)
	}
	return &hmacVerifier{keys: values}, nil
}

func (v *hmacVerifier) Descriptions() []SignatureDescription {
	if v == nil {
		return nil
	}
	output := make([]SignatureDescription, 0, len(v.keys))
	for keyID := range v.keys {
		output = append(output, SignatureDescription{Algorithm: HMACSHA256Algorithm, Profile: HMACSignatureProfileV1, KeyID: keyID})
	}
	slices.SortFunc(output, func(left, right SignatureDescription) int { return strings.Compare(left.KeyID, right.KeyID) })
	return output
}

func (v *hmacVerifier) Verify(ctx context.Context, digest IntegrityDigest, seal Seal) error {
	if v == nil || digest == (IntegrityDigest{}) || seal.Algorithm() != HMACSHA256Algorithm || seal.Profile() != HMACSignatureProfileV1 {
		return auditErrorAt(ErrIntegrity, "signature")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key := v.keys[seal.KeyID()]
	if key == nil {
		return CryptoFailure(CryptoMissingKey, ErrMissingKey)
	}
	mac := hmac.New(sha256.New, key)
	writeFrame(mac, []byte(HMACSignatureProfileV1))
	writeFrame(mac, digest[:])
	if !hmac.Equal(mac.Sum(nil), seal.Bytes()) {
		return auditErrorAt(ErrIntegrity, "signature")
	}
	return nil
}

func cryptoDescriptionError(kind string) error {
	return fmt.Errorf("%w: %s description does not match", ErrDeclaration, kind)
}
