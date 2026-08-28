package storageminio

import (
	"io"
)

// openBody keeps ownership with the caller while projecting errors that occur
// after Open returns through the same bounded adapter taxonomy as immediate
// failures.
type openBody struct {
	body io.ReadCloser
}

func (b *openBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if err == nil || err == io.EOF {
		return n, err
	}
	return n, mapError("open", err, 0, nil)
}

func (b *openBody) Close() error {
	return mapError("open", b.body.Close(), 0, nil)
}
