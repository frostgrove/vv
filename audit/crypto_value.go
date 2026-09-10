package audit

import (
	"bytes"
	"crypto/sha256"
)

type protectedValue struct {
	algorithm  string
	profile    string
	keyID      string
	nonce      []byte
	ciphertext []byte
}

type ProtectedValue struct {
	value protectedValue
}

func NewProtectedValue(algorithm, profile, keyID string, nonce, ciphertext []byte) (ProtectedValue, error) {
	if err := validateProviderDescription(algorithm, profile, keyID); err != nil {
		return ProtectedValue{}, err
	}
	if len(nonce) == 0 || len(nonce) > MaxNameBytes {
		return ProtectedValue{}, auditErrorAt(ErrInvalid, "nonce")
	}
	if len(ciphertext) == 0 {
		return ProtectedValue{}, auditErrorAt(ErrInvalid, "ciphertext")
	}
	if len(ciphertext) > MaxValueBytes+sha256.Size {
		return ProtectedValue{}, auditTooLarge("ciphertext", MaxValueBytes+sha256.Size)
	}
	return ProtectedValue{value: protectedValue{
		algorithm: algorithm, profile: profile, keyID: keyID,
		nonce: bytes.Clone(nonce), ciphertext: bytes.Clone(ciphertext),
	}}, nil
}

func (v ProtectedValue) Algorithm() string { return v.value.algorithm }
func (v ProtectedValue) Profile() string   { return v.value.profile }
func (v ProtectedValue) KeyID() string     { return v.value.keyID }
func (v ProtectedValue) Nonce() []byte     { return bytes.Clone(v.value.nonce) }
func (v ProtectedValue) Ciphertext() []byte {
	return bytes.Clone(v.value.ciphertext)
}

type token struct {
	algorithm string
	profile   string
	keyID     string
	bytes     [sha256.Size]byte
}

type Token struct {
	value token
}

func NewToken(algorithm, profile, keyID string, value []byte) (Token, error) {
	if err := validateProviderDescription(algorithm, profile, keyID); err != nil {
		return Token{}, err
	}
	if len(value) != sha256.Size {
		return Token{}, auditErrorAt(ErrInvalid, "token")
	}
	result := token{algorithm: algorithm, profile: profile, keyID: keyID}
	copy(result.bytes[:], value)
	return Token{value: result}, nil
}

func (v Token) Algorithm() string { return v.value.algorithm }
func (v Token) Profile() string   { return v.value.profile }
func (v Token) KeyID() string     { return v.value.keyID }
func (v Token) Bytes() []byte     { return bytes.Clone(v.value.bytes[:]) }

type seal struct {
	algorithm string
	profile   string
	keyID     string
	bytes     [sha256.Size]byte
}

type Seal struct {
	value seal
}

func NewSeal(algorithm, profile, keyID string, value []byte) (Seal, error) {
	if err := validateProviderDescription(algorithm, profile, keyID); err != nil {
		return Seal{}, err
	}
	if len(value) != sha256.Size {
		return Seal{}, auditErrorAt(ErrInvalid, "seal")
	}
	result := seal{algorithm: algorithm, profile: profile, keyID: keyID}
	copy(result.bytes[:], value)
	return Seal{value: result}, nil
}

func (v Seal) Algorithm() string { return v.value.algorithm }
func (v Seal) Profile() string   { return v.value.profile }
func (v Seal) KeyID() string     { return v.value.keyID }
func (v Seal) Bytes() []byte     { return bytes.Clone(v.value.bytes[:]) }
