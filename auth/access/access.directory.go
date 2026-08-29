package access

import "fmt"

// Directories is every identity store this process serves, indexed by the type
// each one declares for itself.
//
// A map and not a slice, because every lookup in this module is by subject
// type and an index built once at wiring is one fewer linear scan on the
// authenticated path.
type Directories map[SubjectType]Directory

// NewDirectories indexes the implementations an application registered.
//
// It refuses rather than resolves. A directory with no type is a constructor
// somebody left half-written, and two directories claiming one type is a
// composition mistake whose run-time symptom is a caller authenticated against
// the wrong store — resolving it by whichever was passed first would make that
// depend on argument order.
func NewDirectories(all ...Directory) (Directories, error) {
	indexed := make(Directories, len(all))
	for _, directory := range all {
		subjectType := directory.SubjectType()
		if subjectType == "" {
			return nil, fmt.Errorf("access: %T declares an empty subject type", directory)
		}
		if previous, duplicate := indexed[subjectType]; duplicate {
			return nil, fmt.Errorf("access: subject type %q is claimed by both %T and %T",
				subjectType, previous, directory)
		}
		indexed[subjectType] = directory
	}
	return indexed, nil
}

// MustDirectories is [NewDirectories] for a composition root, where the only
// answer to a bad registration is to not start.
func MustDirectories(all ...Directory) Directories {
	indexed, err := NewDirectories(all...)
	if err != nil {
		panic(err)
	}
	return indexed
}

// Directory answers the store behind a subject type, and whether there is one.
func (this Directories) Directory(subjectType SubjectType) (Directory, bool) {
	directory, served := this[subjectType]
	return directory, served
}

var _ DirectoryLookup = Directories(nil)
