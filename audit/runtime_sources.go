package audit

import (
	"crypto/rand"
	"time"
)

type Clock interface {
	Now() time.Time
}

type IDSource interface {
	NewOperationID() (OperationID, error)
	NewRevisionID() (RevisionID, error)
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now().UTC()
}

type cryptoIDSource struct{}

func (cryptoIDSource) NewOperationID() (OperationID, error) {
	var id OperationID
	_, err := rand.Read(id[:])
	if err != nil {
		return OperationID{}, err
	}
	return id, nil
}

func (cryptoIDSource) NewRevisionID() (RevisionID, error) {
	var id RevisionID
	_, err := rand.Read(id[:])
	if err != nil {
		return RevisionID{}, err
	}
	return id, nil
}
