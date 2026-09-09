package vvotel

import "errors"

var ErrInvalidApprovedName = errors.New("vvotel: invalid approved name")

type ApprovedName string

func ApproveName(value string) (ApprovedName, error) {
	if !ValidResourceName(value) {
		return "", ErrInvalidApprovedName
	}
	return ApprovedName(value), nil
}

func MustApproveName(value string) ApprovedName {
	name, err := ApproveName(value)
	if err != nil {
		panic(err)
	}
	return name
}

func (n ApprovedName) Value() string {
	return string(n)
}

func (n ApprovedName) Valid() bool {
	return ValidResourceName(string(n))
}
