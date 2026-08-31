package access

import "fmt"

type Directories map[SubjectType]Directory

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

func MustDirectories(all ...Directory) Directories {
	indexed, err := NewDirectories(all...)
	if err != nil {
		panic(err)
	}
	return indexed
}

func (this Directories) Directory(subjectType SubjectType) (Directory, bool) {
	directory, served := this[subjectType]
	return directory, served
}

var _ DirectoryLookup = Directories(nil)
