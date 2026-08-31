package storageminio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/frostgrove/vv/storage"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (this roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return this(request)
}

func newWireStore(t *testing.T, transport http.RoundTripper) storage.Store {
	t.Helper()
	client, err := minio.New("minio.example", &minio.Options{
		Creds:        credentials.NewStaticV4("access-key", "secret-key", ""),
		Secure:       true,
		Transport:    transport,
		Region:       "us-east-1",
		BucketLookup: minio.BucketLookupPath,
		MaxRetries:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := New(&Config{
		Client: client,
		Bucket: "test-bucket",
		Prefix: "root",
		Clock:  func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	return newTestStore(t, backend)
}

func TestWireCreateOnlyAboveMultipartThresholdIsOneConditionalPUT(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 16*1024*1024+1)
	calls := 0
	var wireErr error
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		switch {
		case request.Method != http.MethodPut:
			wireErr = fmt.Errorf("method = %s", request.Method)
		case request.URL.Path != "/test-bucket/root/tenant/large":
			wireErr = fmt.Errorf("path = %s", request.URL.Path)
		case request.URL.RawQuery != "":
			wireErr = fmt.Errorf("multipart query = %s", request.URL.RawQuery)
		case request.Header.Get("If-None-Match") != "*":
			wireErr = fmt.Errorf("If-None-Match = %q", request.Header.Get("If-None-Match"))
		case request.ContentLength != int64(len(payload)):
			wireErr = fmt.Errorf("Content-Length = %d", request.ContentLength)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil && wireErr == nil {
			wireErr = fmt.Errorf("read request: %w", err)
		}
		if len(body) != len(payload) && wireErr == nil {
			wireErr = fmt.Errorf("body length = %d", len(body))
		}
		if wireErr != nil {
			return nil, wireErr
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header: http.Header{
				"Etag":             {`"wire-etag"`},
				"X-Amz-Version-Id": {"wire-version"},
			},
			Body:    io.NopCloser(bytes.NewReader(nil)),
			Request: request,
		}, nil
	})
	store := newWireStore(t, transport)
	info, err := store.Put(context.Background(), testKey(t, "large"), bytes.NewReader(payload), storage.PutOptions{
		Mode: storage.CreateOnly,
		Size: storage.ExactSize(int64(len(payload))),
	})
	if err != nil {
		t.Fatalf("put: %v (wire error: %v)", err, wireErr)
	}
	if calls != 1 || info.Size != int64(len(payload)) || info.ETag != "wire-etag" || info.Version != "wire-version" {
		t.Fatalf("calls/info = %d/%#v", calls, info)
	}
}

func TestWireOpenUsesImmediateGET(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodGet || request.URL.Path != "/test-bucket/root/tenant/image" {
			return nil, fmt.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			ContentLength: 3,
			Header: http.Header{
				"Content-Length":              {"3"},
				"Content-Type":                {"image/png"},
				"Etag":                        {`"wire-etag"`},
				"Last-Modified":               {testNow.Format(http.TimeFormat)},
				"X-Amz-Meta-Owner":            {"avatar"},
				"X-Amz-Meta-Vv-Stage":         {stageMarkerValue},
				"X-Amz-Meta-Vv-Stage-Expires": {testNow.Add(time.Hour).Format(time.RFC3339Nano)},
			},
			Body:    io.NopCloser(bytes.NewBufferString("abc")),
			Request: request,
		}, nil
	})
	store := newWireStore(t, transport)
	body, info, err := store.Open(context.Background(), testKey(t, "image"))
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	payload, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || string(payload) != "abc" || info.Size != 3 || info.Metadata["owner"] != "avatar" || len(info.Metadata) != 1 {
		t.Fatalf("calls/body/info = %d/%q/%#v", calls, payload, info)
	}
}
