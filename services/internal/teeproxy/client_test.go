package teeproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

const actionID = "0x00000000000000000000000000000000000000000000000000000000000c0de1"

// A real enclave response, in the wire shape the TEE node produces. The decoded
// fields are handed straight to the contract, so the JSON tags have to be right —
// a mismatch would submit empty data with a valid signature and the controller
// would reject it as a binding mismatch, which reads like an enclave bug.
const okBody = `{
  "result": {
    "id": "` + actionID + `",
    "submissionTag": "instruction",
    "status": 1,
    "log": "",
    "opType": "0x5452454153555259000000000000000000000000000000000000000000000000",
    "opCommand": "0x415554484f52495a455f5041594d454e54000000000000000000000000000000",
    "data": "0xdeadbeef"
  },
  "signature": "0xaabbcc",
  "proxySignature": "0x112233"
}`

func serve(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func TestResultDecodesAnEnclaveAnswer(t *testing.T) {
	c := serve(t, http.StatusOK, okBody)

	res, err := c.Result(context.Background(), common.HexToHash(actionID))
	if err != nil {
		t.Fatalf("Result: %v", err)
	}

	if !res.Succeeded() {
		t.Error("status 1 should count as success")
	}
	if got := res.Result.ID.Hex(); got != actionID {
		t.Errorf("id = %s, want %s", got, actionID)
	}
	if res.Result.SubmissionTag != "instruction" {
		t.Errorf("submissionTag = %q, want \"instruction\"", res.Result.SubmissionTag)
	}
	if got := common.Bytes2Hex(res.Result.Data); got != "deadbeef" {
		t.Errorf("data = %s, want deadbeef", got)
	}
	if got := common.Bytes2Hex(res.Signature); got != "aabbcc" {
		t.Errorf("signature = %s, want aabbcc", got)
	}
}

// A pending action is the normal case while the enclave works, not a fault. The
// relay distinguishes it by sentinel so it can re-scan instead of logging noise.
func TestResultReportsNotReadyOn404(t *testing.T) {
	c := serve(t, http.StatusNotFound, `not found`)

	_, err := c.Result(context.Background(), common.HexToHash(actionID))
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v, want ErrNotReady", err)
	}
}

// Without a signature the controller cannot recover the enclave address, so
// submitting would waste gas on a guaranteed revert.
func TestResultRejectsAnUnsignedAnswer(t *testing.T) {
	c := serve(t, http.StatusOK, `{"result":{"id":"`+actionID+`","status":1,"data":"0xdeadbeef"}}`)

	if _, err := c.Result(context.Background(), common.HexToHash(actionID)); err == nil {
		t.Fatal("an unsigned result should be refused")
	}
}

// A signed refusal carries no data and must still be accepted — it is routed to
// submitFailure so the request fails cleanly and releases its reserved budget.
func TestResultAcceptsASignedRefusal(t *testing.T) {
	c := serve(t, http.StatusOK,
		`{"result":{"id":"`+actionID+`","submissionTag":"instruction","status":0,
		  "log":"amount exceeds the per-payment cap","data":"0x"},"signature":"0xaabbcc"}`)

	res, err := c.Result(context.Background(), common.HexToHash(actionID))
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if res.Succeeded() {
		t.Error("status 0 is a refusal, not a success")
	}
	if res.Result.Log == "" {
		t.Error("a refusal should carry the enclave's reason")
	}
}

func TestResultSurfacesServerErrors(t *testing.T) {
	c := serve(t, http.StatusBadGateway, `upstream down`)

	_, err := c.Result(context.Background(), common.HexToHash(actionID))
	if err == nil || errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v, want a real error", err)
	}
}

func TestNewTrimsTrailingSlash(t *testing.T) {
	if got := New("https://example.test/").baseURL; got != "https://example.test" {
		t.Errorf("baseURL = %q, want no trailing slash", got)
	}
}
