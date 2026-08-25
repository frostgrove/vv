package crudhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/port"
	"github.com/shardit-io/vv/query"
	"github.com/shardit-io/vv/remote"
)

// Transport calls a resource another service mounted, over net/http.
//
//	articles := remote.New[Article, int64, ArticleInput](
//	    crudhttp.Transport("https://content.internal/articles"))
//
// baseURL is the prefix the resource was mounted under; the routes below it are
// the ones every HTTP binding registers, so a service on Fiber, on Gin or on
// net/http answers the same calls. There is one HTTP client for that reason: a
// consumer calling out uses net/http whatever it serves with.
//
// It lives here rather than in remote because the tables it needs are here.
// [KindForStatus] is [StatusFor] read backwards and [ParseEnvelope] reads what
// [EnvelopeRenderer] wrote, and a client that kept its own copies of either
// would agree with the server until the first time one of them changed.
func Transport(baseURL string, opts ...TransportOption) remote.Transport {
	t := &transport{base: strings.TrimSuffix(baseURL, "/"), client: http.DefaultClient}
	for _, o := range opts {
		if o != nil {
			o(t)
		}
	}
	return t
}

// A TransportOption wires one part of a [Transport].
type TransportOption func(*transport)

// WithClient replaces the http.Client, which is where a timeout, a transport
// with connection limits, or a instrumented round tripper goes.
func WithClient(c *http.Client) TransportOption {
	return func(t *transport) {
		if c != nil {
			t.client = c
		}
	}
}

// WithRequestHook runs on every request before it is sent — an Authorization
// header, a trace header, an Accept-Language the far end reads with
// [AcceptLanguage]. An error from it fails the call and nothing is sent.
func WithRequestHook(fn func(*http.Request) error) TransportOption {
	return func(t *transport) { t.hook = fn }
}

type transport struct {
	base   string
	client *http.Client
	hook   func(*http.Request) error
}

// Do implements remote.Transport.
func (t *transport) Do(ctx context.Context, call remote.Call) (json.RawMessage, error) {
	method, path, body, err := t.route(call)
	if err != nil {
		return nil, err
	}
	target := t.base + path

	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("crudhttp: building the request for %s: %w", target, err)
	}
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if t.hook != nil {
		if err := t.hook(req); err != nil {
			return nil, err
		}
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("crudhttp: calling %s %s: %w", method, target, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("crudhttp: reading the answer from %s %s: %w", method, target, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return raw, nil
	}
	return nil, fault(call.Method, method+" "+target, resp.Status, resp.StatusCode, raw)
}

// route is the one place a call becomes a verb, a path and a body. The routes
// are crudnet's Mount, and the three bindings register the same ones.
func (t *transport) route(call remote.Call) (method, path string, body []byte, err error) {
	switch call.Method {
	case remote.MethodList:
		body, err = json.Marshal(call.Query)
		return http.MethodPost, "/query", body, err

	case remote.MethodCount:
		body, err = json.Marshal(call.Query)
		return http.MethodPost, "/count", body, err

	case remote.MethodGet:
		q, err := entityQuery(call.Query)
		if err != nil {
			return "", "", nil, err
		}
		return http.MethodGet, "/" + url.PathEscape(call.ID) + q, nil, nil

	case remote.MethodCreate:
		// The collection, which every binding registers with and without the
		// trailing slash. An empty path is the base URL itself.
		return http.MethodPost, "", call.Body, nil

	case remote.MethodUpdate:
		return http.MethodPatch, "/" + url.PathEscape(call.ID), call.Body, nil

	case remote.MethodReplace:
		return http.MethodPut, "/" + url.PathEscape(call.ID), call.Body, nil

	case remote.MethodDelete:
		return http.MethodDelete, "/" + url.PathEscape(call.ID), nil, nil

	case remote.MethodBulkDelete:
		// Assembled rather than marshalled from BulkDeleteRequest, because the
		// keys are already JSON in the caller's own key type and re-encoding
		// them through []string would turn 42 into "42".
		return http.MethodPost, "/bulk-delete", []byte(`{"ids":` + string(call.IDs) + `}`), nil
	}
	return "", "", nil, fmt.Errorf("crudhttp: no route for %s", call.Method)
}

// entityQuery renders the shaping options GET /{id} carries.
//
// Only the projection and the preload paths: port.NarrowForEntity has already
// dropped everything else, because a filter or a page number on the way to one
// row means nothing.
//
// A narrowed preload is refused rather than flattened. The query string carries
// paths and has nowhere to put a per-relation filter, so sending the path alone
// would load every child of the row where the caller asked for some of them —
// more rows than were asked for, over a 200. This is the one place an HTTP
// client can do less than a gRPC one, which sends the whole document.
func entityQuery(req *query.Request) (string, error) {
	if req == nil {
		return "", nil
	}
	v := url.Values{}
	if len(req.Select) > 0 {
		v.Set("select", strings.Join(req.Select, ","))
	}
	paths := make([]string, 0, len(req.Preload))
	for _, p := range req.Preload {
		if !p.Filter.IsZero() || len(p.Sort) > 0 {
			return "", &remote.OptionError{
				Option: "crud.PreloadWhere on GetByID",
				Reason: "the entity route carries preload paths in a query string and has nowhere to put a per-relation filter",
			}
		}
		paths = append(paths, p.Path)
	}
	if len(paths) > 0 {
		v.Set("preload", strings.Join(paths, ","))
	}
	if len(v) == 0 {
		return "", nil
	}
	return "?" + v.Encode(), nil
}

// fault turns a failed response into the error a caller branches on.
//
// A body that is not an envelope is a *remote.ProtocolError and never a
// classified failure, whatever the status said. That is what keeps a wrong base
// URL from arriving as crud.ErrNotFound.
func fault(m remote.Method, where, status string, code int, body []byte) error {
	env, ok := ParseEnvelope(body)
	if !ok {
		return &remote.ProtocolError{
			Method: m,
			Where:  where,
			Status: status,
			Body:   remote.Truncate(strings.TrimSpace(string(body)), 200),
		}
	}
	kind := KindForStatus(code)
	vs := env.Violations()
	return port.FaultFrom(kind, faultCode(vs, kind), vs, env.Partial)
}

// faultCode recovers the fault's own code.
//
// The envelope does not carry it — it carries one per violation — so it is read
// off the first violation in the rendered order, which is where a synthesised
// fault puts a copy of it. That matters for exactly one answer: a stale write
// is CodeStaleVersion, and port.FaultFrom wraps crud.ErrStaleVersion rather
// than the coarse crud.ErrConflict when it sees that code, which is the branch
// a caller re-reads the row from.
//
// A gRPC status carries the fault's code exactly, in ErrorInfo.Reason. This is
// the difference between the two, and it is the envelope's shape rather than a
// choice made here ([[FL-013]]).
func faultCode(vs []errs.Violation, kind errs.Kind) errs.Code {
	for _, v := range vs {
		if v.Code != "" {
			return v.Code
		}
	}
	return port.CodeForKind(kind)
}
