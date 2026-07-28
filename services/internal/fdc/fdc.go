// Package fdc drives a Flare Data Connector attestation from request to proof.
//
// The flow has four steps and one unavoidable wait:
//
//  1. ask a verifier to encode the request (this also computes the message
//     integrity code the protocol needs),
//  2. submit the encoded request to FdcHub with the configured fee,
//  3. wait for the voting round that contains it to be finalised — 90 to 180
//     seconds in practice, because data providers have to reach consensus,
//  4. fetch the response and Merkle proof from the Data Availability layer.
//
// Step 3 is why the UI shows settlement as an asynchronous stage rather than
// pretending a payment is final the moment it lands on XRPL.
package fdc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// Attestation type and source identifiers, right-padded ASCII as FDC encodes them.
var (
	TypeXRPPayment = padTo32("XRPPayment")
	SourceTestXRP  = padTo32("testXRP")
)

func padTo32(s string) string {
	h := hex.EncodeToString([]byte(s))
	return "0x" + h + strings.Repeat("0", 64-len(h))
}

// VotingEpoch parameters for Flare's testnets. A round id is derived from a
// block timestamp, so these must match the network the request was sent on.
const (
	FirstVotingRoundStartTs = 1658430000
	VotingEpochSeconds      = 90
)

// VotingRoundFor returns the FDC round a request submitted at `timestamp` lands in.
func VotingRoundFor(timestamp uint64) uint64 {
	return (timestamp - FirstVotingRoundStartTs) / VotingEpochSeconds
}

// Client talks to the verifier and Data Availability services.
type Client struct {
	verifierURL string
	daURL       string
	apiKey      string
	http        *http.Client
}

// New builds an FDC client.
func New(verifierURL, daURL, apiKey string) *Client {
	return &Client{
		verifierURL: strings.TrimRight(verifierURL, "/"),
		daURL:       strings.TrimRight(daURL, "/"),
		apiKey:      apiKey,
		http:        &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) post(ctx context.Context, url string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshalling request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-KEY", c.apiKey)
	// The verifier and the DA layer disagree about the header's spelling, so send
	// both rather than depending on which service is on the other end.
	req.Header.Set("x-apikey", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("%s returned %d: %s", url, resp.StatusCode, bytes.TrimSpace(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// PrepareXRPPayment asks the verifier to encode an XRPPayment attestation request.
//
// `proofOwner` is the address permitted to use the resulting proof — the
// BridgeSafeFdcVerifier contract, since that is what will submit it.
func (c *Client) PrepareXRPPayment(ctx context.Context, txID, proofOwner string) ([]byte, error) {
	url := c.verifierURL + "/verifier/xrp/XRPPayment/prepareRequest"
	body := map[string]any{
		"attestationType": TypeXRPPayment,
		"sourceId":        SourceTestXRP,
		"requestBody": map[string]any{
			"transactionId": ensure0x(strings.ToLower(txID)),
			"proofOwner":    proofOwner,
		},
	}
	var out struct {
		Status            string `json:"status"`
		ABIEncodedRequest string `json:"abiEncodedRequest"`
	}
	if err := c.post(ctx, url, body, &out); err != nil {
		return nil, err
	}
	if out.Status != "VALID" {
		return nil, fmt.Errorf("verifier rejected the request for %s: status %q — the transaction may not have 3 confirmations yet", txID, out.Status)
	}
	return hex.DecodeString(strings.TrimPrefix(out.ABIEncodedRequest, "0x"))
}

// Proof is an FDC response plus the Merkle path proving it.
type Proof struct {
	MerkleProof [][32]byte
	Response    XRPPaymentResponse
}

// XRPPaymentResponse mirrors IXRPPayment.Response.
type XRPPaymentResponse struct {
	AttestationType     [32]byte
	SourceID            [32]byte
	VotingRound         uint64
	LowestUsedTimestamp uint64
	RequestBody         XRPPaymentRequestBody
	ResponseBody        XRPPaymentResponseBody
}

// XRPPaymentRequestBody mirrors IXRPPayment.RequestBody.
type XRPPaymentRequestBody struct {
	TransactionID [32]byte
	ProofOwner    [20]byte
}

// XRPPaymentResponseBody mirrors IXRPPayment.ResponseBody.
type XRPPaymentResponseBody struct {
	BlockNumber                  uint64
	BlockTimestamp               uint64
	SourceAddress                string
	SourceAddressHash            [32]byte
	ReceivingAddressHash         [32]byte
	IntendedReceivingAddressHash [32]byte
	SpentAmount                  *big.Int
	IntendedSpentAmount          *big.Int
	ReceivedAmount               *big.Int
	IntendedReceivedAmount       *big.Int
	HasMemoData                  bool
	FirstMemoData                []byte
	HasDestinationTag            bool
	DestinationTag               *big.Int
	Status                       uint8
}

// FetchProof retrieves the response and Merkle proof for a finalised round.
//
// Returns a nil proof with no error when the round is finalised but the DA layer
// has not published yet — a normal transient state that the caller polls through.
func (c *Client) FetchProof(ctx context.Context, votingRound uint64, requestBytes []byte) (*Proof, error) {
	url := c.daURL + "/api/v1/fdc/proof-by-request-round"
	body := map[string]any{
		"votingRoundId": votingRound,
		"requestBytes":  "0x" + hex.EncodeToString(requestBytes),
	}

	var out struct {
		Proof    []string        `json:"proof"`
		Response json.RawMessage `json:"response"`
	}
	if err := c.post(ctx, url, body, &out); err != nil {
		// A round that exists but has no published attestation answers 404/400.
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "400") {
			return nil, nil
		}
		return nil, err
	}
	if len(out.Response) == 0 {
		return nil, nil
	}

	resp, err := parseResponse(out.Response)
	if err != nil {
		return nil, err
	}

	proof := &Proof{Response: resp}
	for _, node := range out.Proof {
		b, err := hex.DecodeString(strings.TrimPrefix(node, "0x"))
		if err != nil || len(b) != 32 {
			return nil, fmt.Errorf("malformed Merkle node %q", node)
		}
		var h [32]byte
		copy(h[:], b)
		proof.MerkleProof = append(proof.MerkleProof, h)
	}
	return proof, nil
}

// WaitForProof polls until the proof is published or the context expires.
func (c *Client) WaitForProof(ctx context.Context, votingRound uint64, requestBytes []byte, poll time.Duration) (*Proof, error) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		proof, err := c.FetchProof(ctx, votingRound, requestBytes)
		if err != nil {
			return nil, err
		}
		if proof != nil {
			return proof, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("round %d produced no proof in time: %w", votingRound, ctx.Err())
		case <-ticker.C:
		}
	}
}

func ensure0x(s string) string {
	if strings.HasPrefix(s, "0x") {
		return s
	}
	return "0x" + s
}
