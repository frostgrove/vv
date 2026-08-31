package remotehttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/port/porthttp"
	"github.com/frostgrove/vv/remote"
)

func Transport(baseURL string, options ...TransportOption) remote.Transport {
	t := &transport{base: strings.TrimSuffix(baseURL, "/"), client: defaultClient()}
	for _, o := range options {
		if o != nil {
			o(t)
		}
	}
	return t
}

const DefaultTimeout = 30 * time.Second

const MaxResponse = 32 << 20

func defaultClient() *http.Client { return &http.Client{Timeout: DefaultTimeout} }

type TransportOption func(*transport)

func WithClient(c *http.Client) TransportOption {
	return func(t *transport) {
		if c != nil {
			t.client = c
		}
	}
}

func WithRequestHook(fn func(*http.Request) error) TransportOption {
	return func(t *transport) { t.hook = fn }
}

func WithMaxResponse(n int) TransportOption {
	return func(t *transport) { t.maxResponse = n }
}

type transport struct {
	base        string
	client      *http.Client
	hook        func(*http.Request) error
	maxResponse int
}

func (this *transport) cap() int {
	if this.maxResponse > 0 {
		return this.maxResponse
	}
	return MaxResponse
}

func (this *transport) Do(ctx context.Context, call *remote.Call) (json.RawMessage, error) {
	if call == nil {
		return nil, fmt.Errorf("remotehttp: call is nil")
	}
	method, path, body, err := this.route(call)
	if err != nil {
		return nil, err
	}
	target := this.base + path

	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("remotehttp: building the request for %s: %w", target, err)
	}
	request.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if this.hook != nil {
		if err := this.hook(request); err != nil {
			return nil, err
		}
	}

	response, err := this.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("remotehttp: calling %s %s: %w", method, target, err)
	}
	defer response.Body.Close()

	limit := this.cap()
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("remotehttp: reading the answer from %s %s: %w", method, target, err)
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("remotehttp: the answer to %s %s is larger than the %d bytes this client reads",
			method, target, limit)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return raw, nil
	}
	return nil, fault(call.Method, method+" "+target, response.Status, response.StatusCode, raw)
}

func (this *transport) route(call *remote.Call) (method, path string, body []byte, err error) {
	switch call.Method {
	case remote.MethodGet, remote.MethodUpdate, remote.MethodReplace, remote.MethodDelete:
		if call.ID == "" {
			return "", "", nil, fmt.Errorf("remotehttp: %s requires a non-empty id", call.Method)
		}
	}
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
		return http.MethodPost, "", call.Body, nil

	case remote.MethodUpdate:
		if err := requireMutationBody(call.Method, call.Body); err != nil {
			return "", "", nil, err
		}
		return http.MethodPatch, "/" + url.PathEscape(call.ID), call.Body, nil

	case remote.MethodReplace:
		if err := requireMutationBody(call.Method, call.Body); err != nil {
			return "", "", nil, err
		}
		return http.MethodPut, "/" + url.PathEscape(call.ID), call.Body, nil

	case remote.MethodDelete:
		return http.MethodDelete, "/" + url.PathEscape(call.ID), nil, nil

	case remote.MethodBulkDelete:
		ids := bytes.TrimSpace(call.IDs)
		if len(ids) == 0 {
			ids = []byte("null")
		}
		return http.MethodPost, "/bulk-delete", []byte(`{"ids":` + string(ids) + `}`), nil
	}
	return "", "", nil, fmt.Errorf("remotehttp: no route for %s", call.Method)
}

func requireMutationBody(method remote.Method, body json.RawMessage) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("remotehttp: %s requires a non-null body", method)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return fmt.Errorf("remotehttp: %s body must be a JSON object: %w", method, err)
	}
	return nil
}

func entityQuery(request *query.Request) (string, error) {
	if request == nil {
		return "", nil
	}
	if !request.Filter.IsZero() || len(request.Terms) > 0 || request.Search != "" || len(request.SearchFields) > 0 {
		return "", &remote.OptionError{
			Option: "root eligibility controls on GetByID",
			Reason: "the entity route has no spelling for filter, terms, search, or searchFields; use the List route",
		}
	}
	v := url.Values{}
	if len(request.Select) > 0 {
		v.Set("select", strings.Join(request.Select, ","))
	}
	paths := make([]string, 0, len(request.Preload))
	for _, p := range request.Preload {
		if !p.Filter.IsZero() || len(p.Sort) > 0 || p.MaxRows > 0 {
			return "", &remote.OptionError{
				Option: "narrowed or capped preload on GetByID",
				Reason: "the entity route carries preload paths in a query string and has nowhere to put a per-relation filter, sort, or row cap",
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

func fault(m remote.Method, where, status string, code int, body []byte) error {
	env, ok := porthttp.ParseEnvelope(body)
	if !ok {
		return &remote.ProtocolError{
			Method: m,
			Where:  where,
			Status: status,
			Body:   remote.Truncate(strings.TrimSpace(string(body)), 200),
		}
	}
	kind := porthttp.KindForStatus(code)
	vs := env.Violations()
	return port.FaultFrom(kind, faultCode(vs, kind), vs, env.Partial)
}

func faultCode(vs []errs.Violation, kind errs.Kind) errs.Code {
	for _, v := range vs {
		if v.Code != "" {
			return v.Code
		}
	}
	return port.CodeForKind(kind)
}
