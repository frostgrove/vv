package audit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
)

const (
	HMACSHA256Algorithm    = "hmac-sha256"
	HMACSemanticProfileV1  = "frostgrove.audit.semantic.v1"
	HMACIdentityProfileV1  = "frostgrove.audit.identity.v1"
	HMACTokenProfileV1     = "frostgrove.audit.token.v1"
	HMACSignatureProfileV1 = "frostgrove.audit.signature.v1"
)

type SemanticDigester interface {
	Description() SemanticDigestDescription
	Digest(context.Context, []byte) (SemanticDigest, error)
}

type hmacSemanticDigester struct {
	description SemanticDigestDescription
	key         []byte
}

func HMACSemanticDigester(keyID string, key []byte) (SemanticDigester, error) {
	if !validProviderLabel(keyID) || len(key) < sha256.Size {
		return nil, fmt.Errorf("%w: semantic digest key is invalid", ErrDeclaration)
	}
	return &hmacSemanticDigester{
		description: SemanticDigestDescription{Algorithm: HMACSHA256Algorithm, Profile: HMACSemanticProfileV1, KeyID: keyID},
		key:         bytes.Clone(key),
	}, nil
}

func (d *hmacSemanticDigester) Description() SemanticDigestDescription {
	if d == nil {
		return SemanticDigestDescription{}
	}
	return d.description
}

func (d *hmacSemanticDigester) Digest(ctx context.Context, value []byte) (SemanticDigest, error) {
	if d == nil || len(d.key) < sha256.Size {
		return SemanticDigest{}, fmt.Errorf("%w: semantic digester is invalid", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return SemanticDigest{}, err
	}
	mac := hmac.New(sha256.New, d.key)
	writeFrame(mac, []byte(HMACSemanticProfileV1))
	writeFrame(mac, value)
	var digest SemanticDigest
	copy(digest[:], mac.Sum(nil))
	return digest, nil
}

type IdentityCommitmentDomain uint8

const (
	CommitIdempotency IdentityCommitmentDomain = iota + 1
	CommitEntitySubject
	CommitEvidenceScope
	CommitHoldID
	CommitHoldMatter
	CommitRequester
	CommitAttemptOwner
	CommitAttemptTarget
)

type identityCommitmentRequestOrigin struct{}

type identityCommitmentRequest struct {
	domain    IdentityCommitmentDomain
	plaintext []byte
	aad       []byte
	required  []IdentityCommitmentDescription
	origin    *identityCommitmentRequestOrigin
}

type IdentityCommitmentRequest struct {
	value identityCommitmentRequest
}

type identityCommitment struct {
	description IdentityCommitmentDescription
	bytes       [sha256.Size]byte
}

type IdentityCommitment struct {
	value identityCommitment
}

type identityCommitmentSet struct {
	domain      IdentityCommitmentDomain
	active      IdentityCommitment
	commitments []IdentityCommitment
	origin      *identityCommitmentRequestOrigin
}

type IdentityCommitmentSet struct {
	value identityCommitmentSet
}

type StoredIdentityCommitmentSetData struct {
	Domain      IdentityCommitmentDomain
	Active      IdentityCommitmentDescription
	Commitments []IdentityCommitment
}

type HMACIdentityKey struct {
	KeyID  string
	Key    []byte
	Active bool
}

type IdentityKeyring interface {
	ActiveDescription() IdentityCommitmentDescription
	Descriptions() []IdentityCommitmentDescription
	CommitIdentities(context.Context, IdentityCommitmentRequest) (IdentityCommitmentSet, error)
}

func (r IdentityCommitmentRequest) Domain() IdentityCommitmentDomain {
	return r.value.domain
}

func (r IdentityCommitmentRequest) Plaintext() []byte {
	return bytes.Clone(r.value.plaintext)
}

func (r IdentityCommitmentRequest) AAD() []byte {
	return bytes.Clone(r.value.aad)
}

func (r IdentityCommitmentRequest) Required() []IdentityCommitmentDescription {
	return slices.Clone(r.value.required)
}

func NewIdentityCommitment(description IdentityCommitmentDescription, value []byte) (IdentityCommitment, error) {
	if err := validateProviderDescription(description.Algorithm, description.Profile, description.KeyID); err != nil {
		return IdentityCommitment{}, err
	}
	if len(value) != sha256.Size {
		return IdentityCommitment{}, fmt.Errorf("%w: identity commitment must be %d bytes", ErrInvalid, sha256.Size)
	}
	commitment := identityCommitment{description: description}
	copy(commitment.bytes[:], value)
	return IdentityCommitment{value: commitment}, nil
}

func NewIdentityCommitmentSet(request IdentityCommitmentRequest, commitments []IdentityCommitment) (IdentityCommitmentSet, error) {
	if request.value.origin == nil || !validIdentityDomain(request.value.domain) {
		return IdentityCommitmentSet{}, fmt.Errorf("%w: identity commitment request is invalid", ErrInvalid)
	}
	return buildIdentityCommitmentSet(request.value.domain, request.value.required, commitments, request.value.origin)
}

func NewStoredIdentityCommitmentSet(data StoredIdentityCommitmentSetData) (IdentityCommitmentSet, error) {
	if !validIdentityDomain(data.Domain) {
		return IdentityCommitmentSet{}, fmt.Errorf("%w: stored identity commitment domain is invalid", ErrMalformedEvidence)
	}
	if len(data.Commitments) == 0 {
		return IdentityCommitmentSet{}, fmt.Errorf("%w: stored identity commitment set is empty", ErrMalformedEvidence)
	}
	required := make([]IdentityCommitmentDescription, len(data.Commitments))
	for index, commitment := range data.Commitments {
		required[index] = commitment.value.description
	}
	set, err := buildIdentityCommitmentSet(data.Domain, required, data.Commitments, nil)
	if err != nil {
		return IdentityCommitmentSet{}, fmt.Errorf("%w: stored identity commitment set is invalid", ErrMalformedEvidence)
	}
	if set.value.active.value.description != data.Active {
		for _, commitment := range set.value.commitments {
			if commitment.value.description == data.Active {
				set.value.active = commitment
				return set, nil
			}
		}
		return IdentityCommitmentSet{}, fmt.Errorf("%w: stored active identity commitment is absent", ErrMalformedEvidence)
	}
	return set, nil
}

func (c IdentityCommitment) Description() IdentityCommitmentDescription {
	return c.value.description
}

func (c IdentityCommitment) Bytes() []byte {
	return bytes.Clone(c.value.bytes[:])
}

func (s IdentityCommitmentSet) Active() IdentityCommitment {
	return s.value.active
}

func (s IdentityCommitmentSet) Aliases() []IdentityCommitment {
	return slices.Clone(s.value.commitments)
}

func (s IdentityCommitmentSet) Domain() IdentityCommitmentDomain {
	return s.value.domain
}

type hmacIdentityKey struct {
	description IdentityCommitmentDescription
	key         []byte
	active      bool
}

type hmacIdentityKeyring struct {
	keys   []hmacIdentityKey
	active IdentityCommitmentDescription
}

func HMACIdentityKeyring(keys ...HMACIdentityKey) (IdentityKeyring, error) {
	if len(keys) == 0 || len(keys) > MaxCatalogs {
		return nil, fmt.Errorf("%w: identity key count is invalid", ErrDeclaration)
	}
	values := make([]hmacIdentityKey, len(keys))
	seen := make(map[string]struct{}, len(keys))
	activeCount := 0
	var active IdentityCommitmentDescription
	for index, key := range keys {
		if !validProviderLabel(key.KeyID) || len(key.Key) < sha256.Size {
			return nil, fmt.Errorf("%w: identity key is invalid", ErrDeclaration)
		}
		if _, duplicate := seen[key.KeyID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate identity key", ErrDeclaration)
		}
		seen[key.KeyID] = struct{}{}
		description := IdentityCommitmentDescription{Algorithm: HMACSHA256Algorithm, Profile: HMACIdentityProfileV1, KeyID: key.KeyID}
		values[index] = hmacIdentityKey{description: description, key: bytes.Clone(key.Key), active: key.Active}
		if key.Active {
			activeCount++
			active = description
		}
	}
	if activeCount != 1 {
		return nil, fmt.Errorf("%w: identity keyring requires exactly one active key", ErrDeclaration)
	}
	slices.SortFunc(values, func(left, right hmacIdentityKey) int {
		return strings.Compare(left.description.KeyID, right.description.KeyID)
	})
	return &hmacIdentityKeyring{keys: values, active: active}, nil
}

func (k *hmacIdentityKeyring) ActiveDescription() IdentityCommitmentDescription {
	if k == nil {
		return IdentityCommitmentDescription{}
	}
	return k.active
}

func (k *hmacIdentityKeyring) Descriptions() []IdentityCommitmentDescription {
	if k == nil {
		return nil
	}
	result := make([]IdentityCommitmentDescription, len(k.keys))
	for index, key := range k.keys {
		result[index] = key.description
	}
	return result
}

func (k *hmacIdentityKeyring) CommitIdentities(ctx context.Context, request IdentityCommitmentRequest) (IdentityCommitmentSet, error) {
	if k == nil || request.value.origin == nil || !validIdentityDomain(request.value.domain) {
		return IdentityCommitmentSet{}, fmt.Errorf("%w: identity commitment request is invalid", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return IdentityCommitmentSet{}, err
	}
	required := request.value.required
	if len(required) == 0 {
		required = []IdentityCommitmentDescription{k.active}
		for _, description := range k.Descriptions() {
			if description != k.active {
				required = append(required, description)
			}
		}
	}
	commitments := make([]IdentityCommitment, 0, len(required))
	for _, description := range required {
		var key *hmacIdentityKey
		for index := range k.keys {
			if k.keys[index].description == description {
				key = &k.keys[index]
				break
			}
		}
		if key == nil {
			return IdentityCommitmentSet{}, fmt.Errorf("%w: required identity key is unavailable", ErrMissingKey)
		}
		mac := hmac.New(sha256.New, key.key)
		writeFrame(mac, []byte(HMACIdentityProfileV1))
		writeFrame(mac, []byte{byte(request.value.domain)})
		writeFrame(mac, []byte(description.Algorithm))
		writeFrame(mac, []byte(description.Profile))
		writeFrame(mac, []byte(description.KeyID))
		writeFrame(mac, request.value.aad)
		writeFrame(mac, request.value.plaintext)
		commitment, _ := NewIdentityCommitment(description, mac.Sum(nil))
		commitments = append(commitments, commitment)
	}
	return NewIdentityCommitmentSet(request, commitments)
}

func newIdentityCommitmentRequest(domain IdentityCommitmentDomain, plaintext, aad []byte, required []IdentityCommitmentDescription) (IdentityCommitmentRequest, error) {
	if !validIdentityDomain(domain) || len(plaintext) == 0 || len(plaintext) > MaxReconcileKeyBytes || len(aad) > MaxReconcileKeyBytes {
		return IdentityCommitmentRequest{}, fmt.Errorf("%w: identity commitment request is invalid", ErrInvalid)
	}
	copy := slices.Clone(required)
	seen := make(map[IdentityCommitmentDescription]struct{}, len(copy))
	for _, description := range copy {
		if err := validateProviderDescription(description.Algorithm, description.Profile, description.KeyID); err != nil {
			return IdentityCommitmentRequest{}, err
		}
		if _, duplicate := seen[description]; duplicate {
			return IdentityCommitmentRequest{}, fmt.Errorf("%w: duplicate required identity description", ErrInvalid)
		}
		seen[description] = struct{}{}
	}
	return IdentityCommitmentRequest{value: identityCommitmentRequest{
		domain: domain, plaintext: bytes.Clone(plaintext), aad: bytes.Clone(aad), required: copy, origin: &identityCommitmentRequestOrigin{},
	}}, nil
}

func buildIdentityCommitmentSet(domain IdentityCommitmentDomain, required []IdentityCommitmentDescription, commitments []IdentityCommitment, origin *identityCommitmentRequestOrigin) (IdentityCommitmentSet, error) {
	if len(commitments) == 0 || len(commitments) != len(required) {
		return IdentityCommitmentSet{}, fmt.Errorf("%w: identity commitment inventory is incomplete", ErrInvalid)
	}
	values := slices.Clone(commitments)
	slices.SortFunc(values, func(left, right IdentityCommitment) int {
		return compareIdentityDescription(left.value.description, right.value.description)
	})
	need := slices.Clone(required)
	slices.SortFunc(need, compareIdentityDescription)
	var active IdentityCommitment
	for index, commitment := range values {
		if commitment.value.bytes == ([sha256.Size]byte{}) || commitment.value.description != need[index] {
			return IdentityCommitmentSet{}, fmt.Errorf("%w: identity commitment inventory does not match the request", ErrInvalid)
		}
		if index > 0 && commitment.value.description == values[index-1].value.description {
			return IdentityCommitmentSet{}, fmt.Errorf("%w: duplicate identity commitment", ErrInvalid)
		}
		if commitment.value.description == required[0] {
			active = commitment
		}
	}
	if active.value.description == (IdentityCommitmentDescription{}) {
		return IdentityCommitmentSet{}, fmt.Errorf("%w: active identity commitment is absent", ErrInvalid)
	}
	return IdentityCommitmentSet{value: identityCommitmentSet{domain: domain, active: active, commitments: values, origin: origin}}, nil
}

func compareIdentityDescription(left, right IdentityCommitmentDescription) int {
	if left.Algorithm != right.Algorithm {
		return strings.Compare(left.Algorithm, right.Algorithm)
	}
	if left.Profile != right.Profile {
		return strings.Compare(left.Profile, right.Profile)
	}
	return strings.Compare(left.KeyID, right.KeyID)
}

func validIdentityDomain(domain IdentityCommitmentDomain) bool {
	return domain >= CommitIdempotency && domain <= CommitAttemptTarget
}
