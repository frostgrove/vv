package storageminio

import (
	"io"
)

type openBody struct {
	body io.ReadCloser
}

func (this *openBody) Read(p []byte) (int, error) {
	n, err := this.body.Read(p)
	if err == nil || err == io.EOF {
		return n, err
	}
	return n, mapError("open", err, 0, nil)
}

func (this *openBody) Close() error {
	return mapError("open", this.body.Close(), 0, nil)
}
