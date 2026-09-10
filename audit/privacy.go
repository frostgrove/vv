package audit

import (
	"crypto/sha256"
	"fmt"
	"slices"
)

type privacyAdmission struct {
	reason    PrivacyReason
	plaintext []Classification
}

type PrivacyAdmission struct {
	value privacyAdmission
}

type PrivacyAdmissionView struct {
	Reason    PrivacyReason
	Plaintext []Classification
}

type DeploymentFingerprint [32]byte

func AdmitPlaintext(reason PrivacyReason, classifications ...Classification) (PrivacyAdmission, error) {
	if err := validateCodecName(string(reason)); err != nil {
		return PrivacyAdmission{}, fmt.Errorf("%w: privacy admission reason is invalid", ErrAdmission)
	}
	if len(classifications) == 0 {
		return PrivacyAdmission{}, fmt.Errorf("%w: privacy admission widens no classification", ErrAdmission)
	}
	values := slices.Clone(classifications)
	slices.Sort(values)
	for index, classification := range values {
		if classification != Personal || index > 0 && classification == values[index-1] {
			return PrivacyAdmission{}, fmt.Errorf("%w: only personal plaintext can be admitted", ErrAdmission)
		}
	}
	return PrivacyAdmission{value: privacyAdmission{reason: reason, plaintext: values}}, nil
}

func (p PrivacyAdmission) View() PrivacyAdmissionView {
	return PrivacyAdmissionView{Reason: p.value.reason, Plaintext: slices.Clone(p.value.plaintext)}
}

func (p PrivacyAdmission) Fingerprint() DeploymentFingerprint {
	hash := sha256.New()
	writeFrame(hash, []byte("frostgrove.audit.privacy-admission/v1"))
	writeFrame(hash, []byte(p.value.reason))
	writeUint32(hash, uint32(len(p.value.plaintext)))
	for _, classification := range p.value.plaintext {
		writeUint32(hash, uint32(classification))
	}
	var fingerprint DeploymentFingerprint
	copy(fingerprint[:], hash.Sum(nil))
	return fingerprint
}

func (p PrivacyAdmission) allows(classification Classification, mode StorageMode) bool {
	if mode != AsPlaintext {
		return true
	}
	if classification == Public || classification == Internal {
		return true
	}
	return classification == Personal && slices.Contains(p.value.plaintext, Personal)
}
