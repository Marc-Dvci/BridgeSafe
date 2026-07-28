// Package teeproxy reads completed action results from a Flare Compute Extension
// proxy.
//
// The enclave answers an instruction asynchronously: the contract dispatches it,
// the TEE node executes it, and the signed result waits at
// `GET /action/result/{actionId}` until someone collects it. Nothing pushes it
// back on chain, which is what result-relay exists to do.
//
// The response shape is Flare's `tee-node/pkg/types.ActionResponse`. It is
// restated here rather than imported so the services module keeps its small
// dependency tree — the same reasoning as the hand-written ABI fragments next
// door. The fields below are the whole of what BridgeSafe reads.
package teeproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// Result is one completed enclave action.
type Result struct {
	ID            common.Hash   `json:"id"`
	SubmissionTag string        `json:"submissionTag"`
	Status        uint8         `json:"status"`
	Log           string        `json:"log"`
	OPType        common.Hash   `json:"opType"`
	OPCommand     common.Hash   `json:"opCommand"`
	Data          hexutil.Bytes `json:"data"`
}

// Response wraps a result with the enclave's signature over it.
type Response struct {
	Result         Result        `json:"result"`
	Signature      hexutil.Bytes `json:"signature"`
	ProxySignature hexutil.Bytes `json:"proxySignature"`
}

// Succeeded reports whether the enclave carried the instruction out.
//
// The enclave uses 1 for success and 0 for a refusal it still signs — a refusal
// is a real, attributable answer, not an error, and the contract records it via
// submitFailure.
func (r *Response) Succeeded() bool { return r.Result.Status == 1 }

// ErrNotReady means the enclave has not finished this action yet.
var ErrNotReady = fmt.Errorf("action result not ready")

// Client fetches results from one extension proxy.
type Client struct {
	baseURL string
	http    *http.Client
}

// New builds a client for a proxy base URL, e.g. the cloudflared tunnel address.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

// Result fetches one action result.
//
// A 404 means the enclave is still working, which during normal operation is the
// common case rather than a fault — the caller polls rather than failing.
func (c *Client) Result(ctx context.Context, actionID common.Hash) (*Response, error) {
	url := fmt.Sprintf("%s/action/result/%s", c.baseURL, actionID.Hex())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying the extension proxy: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return nil, ErrNotReady
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("extension proxy returned %d for %s", res.StatusCode, actionID.Hex())
	}

	var out Response
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding action result: %w", err)
	}

	// An unsigned result is unusable: every controller entry point recovers the
	// enclave address from this signature before it changes state.
	if len(out.Signature) == 0 {
		return nil, fmt.Errorf("action %s came back without a signature", actionID.Hex())
	}
	if len(out.Result.Data) == 0 && out.Succeeded() {
		return nil, fmt.Errorf("action %s succeeded with no result data", actionID.Hex())
	}

	return &out, nil
}

// Info reports the proxy's machine data, used at startup to confirm the enclave
// the relay is talking to is the one the controller trusts.
func (c *Client) Info(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/info", nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying the extension proxy: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("extension proxy /info returned %d", res.StatusCode)
	}

	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding /info: %w", err)
	}
	return out, nil
}
