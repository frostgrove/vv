package auditpg

import (
	"encoding/json"
	"fmt"

	"github.com/frostgrove/vv/audit"
)

type providerBytesWire struct {
	Algorithm string `json:"algorithm,omitempty"`
	Profile   string `json:"profile,omitempty"`
	KeyID     string `json:"key_id,omitempty"`
	Bytes     []byte `json:"bytes,omitempty"`
}

type protectedWire struct {
	Algorithm  string `json:"algorithm,omitempty"`
	Profile    string `json:"profile,omitempty"`
	KeyID      string `json:"key_id,omitempty"`
	Nonce      []byte `json:"nonce,omitempty"`
	Ciphertext []byte `json:"ciphertext,omitempty"`
}

type storedValueSecrets struct {
	Token     providerBytesWire `json:"token,omitempty"`
	Protected protectedWire     `json:"protected,omitempty"`
}

type identityWire struct {
	Description audit.IdentityCommitmentDescription `json:"description"`
	Bytes       []byte                              `json:"bytes"`
}

type identitySetWire struct {
	Domain  audit.IdentityCommitmentDomain      `json:"domain,omitempty"`
	Active  audit.IdentityCommitmentDescription `json:"active,omitempty"`
	Aliases []identityWire                      `json:"aliases,omitempty"`
}

type coordinateAlternativeSecrets struct {
	Tokens      []providerBytesWire `json:"tokens,omitempty"`
	Commitments identitySetWire     `json:"commitments,omitempty"`
}

type coordinateSecrets struct {
	Alternatives []coordinateAlternativeSecrets `json:"alternatives,omitempty"`
}

type itemSecrets struct {
	Subject storedValueSecrets   `json:"subject,omitempty"`
	Target  storedValueSecrets   `json:"target,omitempty"`
	Values  []storedValueSecrets `json:"values,omitempty"`
	Before  []storedValueSecrets `json:"before,omitempty"`
	After   []storedValueSecrets `json:"after,omitempty"`
	Matter  storedValueSecrets   `json:"matter,omitempty"`
}

type revisionEvidence struct {
	Version     uint16                 `json:"version"`
	Revision    audit.RevisionWireView `json:"revision"`
	Seal        providerBytesWire      `json:"seal,omitempty"`
	Actors      []storedValueSecrets   `json:"actors,omitempty"`
	Context     []storedValueSecrets   `json:"context,omitempty"`
	Items       []itemSecrets          `json:"items"`
	Coordinates []coordinateSecrets    `json:"coordinates,omitempty"`
}

func encodeEvidence(view audit.RevisionWireView) ([]byte, error) {
	evidence := revisionEvidence{
		Version: 1, Revision: view, Seal: sealWire(view.Header.Seal),
		Actors: make([]storedValueSecrets, len(view.Actors)), Context: make([]storedValueSecrets, len(view.Context)),
		Items: make([]itemSecrets, len(view.Items)), Coordinates: make([]coordinateSecrets, len(view.Header.Authorization.Coordinates)),
	}
	for index, actor := range view.Actors {
		evidence.Actors[index] = valueSecrets(actor.Reference)
	}
	for index, fact := range view.Context {
		evidence.Context[index] = directSecrets(fact.Token, fact.Protected)
	}
	for index, item := range view.Items {
		secrets := itemSecrets{
			Subject: valueSecrets(item.Subject), Target: valueSecrets(item.Target), Matter: valueSecrets(item.Hold.Matter),
			Values: make([]storedValueSecrets, len(item.Values)), Before: make([]storedValueSecrets, len(item.Changes)), After: make([]storedValueSecrets, len(item.Changes)),
		}
		for valueIndex, value := range item.Values {
			secrets.Values[valueIndex] = valueSecrets(value)
		}
		for changeIndex, change := range item.Changes {
			secrets.Before[changeIndex] = valueSecrets(change.Before)
			secrets.After[changeIndex] = valueSecrets(change.After)
		}
		evidence.Items[index] = secrets
	}
	for index, coordinate := range view.Header.Authorization.Coordinates {
		secrets := coordinateSecrets{Alternatives: make([]coordinateAlternativeSecrets, len(coordinate.Alternatives))}
		for alternativeIndex, alternative := range coordinate.Alternatives {
			values := make([]providerBytesWire, len(alternative.Tokens))
			for tokenIndex, token := range alternative.Tokens {
				values[tokenIndex] = tokenWire(token)
			}
			secrets.Alternatives[alternativeIndex] = coordinateAlternativeSecrets{Tokens: values, Commitments: identitySet(alternative.Commitments)}
		}
		evidence.Coordinates[index] = secrets
	}
	wire, err := json.Marshal(evidence)
	if err != nil {
		return nil, fmt.Errorf("auditpg: encode revision: %w", err)
	}
	return wire, nil
}

func decodeEvidence(wire []byte) (audit.RevisionWireView, error) {
	var evidence revisionEvidence
	if err := json.Unmarshal(wire, &evidence); err != nil {
		return audit.RevisionWireView{}, fmt.Errorf("auditpg: decode revision: %w", err)
	}
	view := evidence.Revision
	if evidence.Version != 1 || len(evidence.Actors) != len(view.Actors) || len(evidence.Context) != len(view.Context) || len(evidence.Items) != len(view.Items) || len(evidence.Coordinates) != len(view.Header.Authorization.Coordinates) {
		return audit.RevisionWireView{}, errorsWire("revision shape")
	}
	seal, err := decodeSeal(evidence.Seal)
	if err != nil {
		return audit.RevisionWireView{}, err
	}
	view.Header.Seal = seal
	for index := range view.Actors {
		if err := restoreValue(&view.Actors[index].Reference, evidence.Actors[index]); err != nil {
			return audit.RevisionWireView{}, err
		}
	}
	for index := range view.Context {
		token, protected, err := restoreDirect(evidence.Context[index])
		if err != nil {
			return audit.RevisionWireView{}, err
		}
		view.Context[index].Token, view.Context[index].Protected = token, protected
	}
	for index := range view.Items {
		item, secrets := &view.Items[index], evidence.Items[index]
		if len(secrets.Values) != len(item.Values) || len(secrets.Before) != len(item.Changes) || len(secrets.After) != len(item.Changes) {
			return audit.RevisionWireView{}, errorsWire("item shape")
		}
		for target, secret := range map[*audit.StoredValueView]storedValueSecrets{
			&item.Subject: secrets.Subject, &item.Target: secrets.Target, &item.Hold.Matter: secrets.Matter,
		} {
			if err := restoreValue(target, secret); err != nil {
				return audit.RevisionWireView{}, err
			}
		}
		for valueIndex := range item.Values {
			if err := restoreValue(&item.Values[valueIndex], secrets.Values[valueIndex]); err != nil {
				return audit.RevisionWireView{}, err
			}
		}
		for changeIndex := range item.Changes {
			if err := restoreValue(&item.Changes[changeIndex].Before, secrets.Before[changeIndex]); err != nil {
				return audit.RevisionWireView{}, err
			}
			if err := restoreValue(&item.Changes[changeIndex].After, secrets.After[changeIndex]); err != nil {
				return audit.RevisionWireView{}, err
			}
		}
	}
	for index := range view.Header.Authorization.Coordinates {
		coordinate, secrets := &view.Header.Authorization.Coordinates[index], evidence.Coordinates[index]
		if len(coordinate.Alternatives) != len(secrets.Alternatives) {
			return audit.RevisionWireView{}, errorsWire("coordinate shape")
		}
		for alternativeIndex := range coordinate.Alternatives {
			alternative, secret := &coordinate.Alternatives[alternativeIndex], secrets.Alternatives[alternativeIndex]
			if len(alternative.Tokens) != len(secret.Tokens) {
				return audit.RevisionWireView{}, errorsWire("coordinate token shape")
			}
			for tokenIndex := range alternative.Tokens {
				token, err := decodeToken(secret.Tokens[tokenIndex])
				if err != nil {
					return audit.RevisionWireView{}, err
				}
				alternative.Tokens[tokenIndex] = token
			}
			set, err := decodeIdentitySet(secret.Commitments)
			if err != nil {
				return audit.RevisionWireView{}, err
			}
			alternative.Commitments = set
		}
	}
	return view, nil
}

func valueSecrets(value audit.StoredValueView) storedValueSecrets {
	return directSecrets(value.Token, value.Protected)
}

func directSecrets(token audit.Token, protected audit.ProtectedValue) storedValueSecrets {
	return storedValueSecrets{Token: tokenWire(token), Protected: protectionWire(protected)}
}

func tokenWire(value audit.Token) providerBytesWire {
	if value.Algorithm() == "" {
		return providerBytesWire{}
	}
	return providerBytesWire{Algorithm: value.Algorithm(), Profile: value.Profile(), KeyID: value.KeyID(), Bytes: value.Bytes()}
}

func sealWire(value audit.Seal) providerBytesWire {
	if value.Algorithm() == "" {
		return providerBytesWire{}
	}
	return providerBytesWire{Algorithm: value.Algorithm(), Profile: value.Profile(), KeyID: value.KeyID(), Bytes: value.Bytes()}
}

func protectionWire(value audit.ProtectedValue) protectedWire {
	if value.Algorithm() == "" {
		return protectedWire{}
	}
	return protectedWire{Algorithm: value.Algorithm(), Profile: value.Profile(), KeyID: value.KeyID(), Nonce: value.Nonce(), Ciphertext: value.Ciphertext()}
}

func identitySet(value audit.IdentityCommitmentSet) identitySetWire {
	if value.Domain() == 0 {
		return identitySetWire{}
	}
	aliases := value.Aliases()
	wire := identitySetWire{Domain: value.Domain(), Active: value.Active().Description(), Aliases: make([]identityWire, len(aliases))}
	for index, alias := range aliases {
		wire.Aliases[index] = identityWire{Description: alias.Description(), Bytes: alias.Bytes()}
	}
	return wire
}

func restoreValue(target *audit.StoredValueView, secret storedValueSecrets) error {
	token, protected, err := restoreDirect(secret)
	if err != nil {
		return err
	}
	target.Token, target.Protected = token, protected
	return nil
}

func restoreDirect(secret storedValueSecrets) (audit.Token, audit.ProtectedValue, error) {
	token, err := decodeToken(secret.Token)
	if err != nil {
		return audit.Token{}, audit.ProtectedValue{}, err
	}
	protected, err := decodeProtected(secret.Protected)
	if err != nil {
		return audit.Token{}, audit.ProtectedValue{}, err
	}
	return token, protected, nil
}

func decodeToken(wire providerBytesWire) (audit.Token, error) {
	if wire.Algorithm == "" && wire.Profile == "" && wire.KeyID == "" && len(wire.Bytes) == 0 {
		return audit.Token{}, nil
	}
	value, err := audit.NewToken(wire.Algorithm, wire.Profile, wire.KeyID, wire.Bytes)
	if err != nil {
		return audit.Token{}, errorsWire("token")
	}
	return value, nil
}

func decodeSeal(wire providerBytesWire) (audit.Seal, error) {
	if wire.Algorithm == "" && wire.Profile == "" && wire.KeyID == "" && len(wire.Bytes) == 0 {
		return audit.Seal{}, nil
	}
	value, err := audit.NewSeal(wire.Algorithm, wire.Profile, wire.KeyID, wire.Bytes)
	if err != nil {
		return audit.Seal{}, errorsWire("seal")
	}
	return value, nil
}

func decodeProtected(wire protectedWire) (audit.ProtectedValue, error) {
	if wire.Algorithm == "" && wire.Profile == "" && wire.KeyID == "" && len(wire.Nonce) == 0 && len(wire.Ciphertext) == 0 {
		return audit.ProtectedValue{}, nil
	}
	value, err := audit.NewProtectedValue(wire.Algorithm, wire.Profile, wire.KeyID, wire.Nonce, wire.Ciphertext)
	if err != nil {
		return audit.ProtectedValue{}, errorsWire("protected value")
	}
	return value, nil
}

func decodeIdentitySet(wire identitySetWire) (audit.IdentityCommitmentSet, error) {
	if wire.Domain == 0 && len(wire.Aliases) == 0 && wire.Active == (audit.IdentityCommitmentDescription{}) {
		return audit.IdentityCommitmentSet{}, nil
	}
	aliases := make([]audit.IdentityCommitment, len(wire.Aliases))
	for index, alias := range wire.Aliases {
		value, err := audit.NewIdentityCommitment(alias.Description, alias.Bytes)
		if err != nil {
			return audit.IdentityCommitmentSet{}, errorsWire("identity commitment")
		}
		aliases[index] = value
	}
	set, err := audit.NewStoredIdentityCommitmentSet(audit.StoredIdentityCommitmentSetData{Domain: wire.Domain, Active: wire.Active, Commitments: aliases})
	if err != nil {
		return audit.IdentityCommitmentSet{}, errorsWire("identity commitment set")
	}
	return set, nil
}

func errorsWire(part string) error {
	return fmt.Errorf("%w: stored %s is malformed", ErrSchemaMismatch, part)
}
