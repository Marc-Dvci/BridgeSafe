package fdc

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// daResponse is the JSON shape the Data Availability layer returns.
//
// Numeric fields arrive as decimal strings because they exceed JavaScript's safe
// integer range, so every one is parsed explicitly rather than through
// encoding/json's number handling.
type daResponse struct {
	AttestationType     string `json:"attestationType"`
	SourceID            string `json:"sourceId"`
	VotingRound         string `json:"votingRound"`
	LowestUsedTimestamp string `json:"lowestUsedTimestamp"`
	RequestBody         struct {
		TransactionID string `json:"transactionId"`
		ProofOwner    string `json:"proofOwner"`
	} `json:"requestBody"`
	ResponseBody struct {
		BlockNumber                  string `json:"blockNumber"`
		BlockTimestamp               string `json:"blockTimestamp"`
		SourceAddress                string `json:"sourceAddress"`
		SourceAddressHash            string `json:"sourceAddressHash"`
		ReceivingAddressHash         string `json:"receivingAddressHash"`
		IntendedReceivingAddressHash string `json:"intendedReceivingAddressHash"`
		SpentAmount                  string `json:"spentAmount"`
		IntendedSpentAmount          string `json:"intendedSpentAmount"`
		ReceivedAmount               string `json:"receivedAmount"`
		IntendedReceivedAmount       string `json:"intendedReceivedAmount"`
		HasMemoData                  bool   `json:"hasMemoData"`
		FirstMemoData                string `json:"firstMemoData"`
		HasDestinationTag            bool   `json:"hasDestinationTag"`
		DestinationTag               string `json:"destinationTag"`
		Status                       string `json:"status"`
	} `json:"responseBody"`
}

func parseResponse(raw json.RawMessage) (XRPPaymentResponse, error) {
	var r daResponse
	var out XRPPaymentResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return out, fmt.Errorf("decoding DA response: %w", err)
	}

	var err error
	if out.AttestationType, err = hash32(r.AttestationType); err != nil {
		return out, fmt.Errorf("attestationType: %w", err)
	}
	if out.SourceID, err = hash32(r.SourceID); err != nil {
		return out, fmt.Errorf("sourceId: %w", err)
	}
	if out.VotingRound, err = num(r.VotingRound); err != nil {
		return out, fmt.Errorf("votingRound: %w", err)
	}
	if out.LowestUsedTimestamp, err = num(r.LowestUsedTimestamp); err != nil {
		return out, fmt.Errorf("lowestUsedTimestamp: %w", err)
	}
	if out.RequestBody.TransactionID, err = hash32(r.RequestBody.TransactionID); err != nil {
		return out, fmt.Errorf("transactionId: %w", err)
	}
	if out.RequestBody.ProofOwner, err = addr20(r.RequestBody.ProofOwner); err != nil {
		return out, fmt.Errorf("proofOwner: %w", err)
	}

	b := &out.ResponseBody
	src := r.ResponseBody
	if b.BlockNumber, err = num(src.BlockNumber); err != nil {
		return out, fmt.Errorf("blockNumber: %w", err)
	}
	if b.BlockTimestamp, err = num(src.BlockTimestamp); err != nil {
		return out, fmt.Errorf("blockTimestamp: %w", err)
	}
	b.SourceAddress = src.SourceAddress
	if b.SourceAddressHash, err = hash32(src.SourceAddressHash); err != nil {
		return out, fmt.Errorf("sourceAddressHash: %w", err)
	}
	if b.ReceivingAddressHash, err = hash32(src.ReceivingAddressHash); err != nil {
		return out, fmt.Errorf("receivingAddressHash: %w", err)
	}
	if b.IntendedReceivingAddressHash, err = hash32(src.IntendedReceivingAddressHash); err != nil {
		return out, fmt.Errorf("intendedReceivingAddressHash: %w", err)
	}
	if b.SpentAmount, err = bigint(src.SpentAmount); err != nil {
		return out, fmt.Errorf("spentAmount: %w", err)
	}
	if b.IntendedSpentAmount, err = bigint(src.IntendedSpentAmount); err != nil {
		return out, fmt.Errorf("intendedSpentAmount: %w", err)
	}
	if b.ReceivedAmount, err = bigint(src.ReceivedAmount); err != nil {
		return out, fmt.Errorf("receivedAmount: %w", err)
	}
	if b.IntendedReceivedAmount, err = bigint(src.IntendedReceivedAmount); err != nil {
		return out, fmt.Errorf("intendedReceivedAmount: %w", err)
	}
	b.HasMemoData = src.HasMemoData
	if b.FirstMemoData, err = hexBytes(src.FirstMemoData); err != nil {
		return out, fmt.Errorf("firstMemoData: %w", err)
	}
	b.HasDestinationTag = src.HasDestinationTag
	if b.DestinationTag, err = bigint(src.DestinationTag); err != nil {
		return out, fmt.Errorf("destinationTag: %w", err)
	}
	status, err := num(src.Status)
	if err != nil {
		return out, fmt.Errorf("status: %w", err)
	}
	b.Status = uint8(status)

	return out, nil
}

func hash32(s string) ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		return out, err
	}
	if len(b) != 32 {
		return out, fmt.Errorf("expected 32 bytes, got %d", len(b))
	}
	copy(out[:], b)
	return out, nil
}

func addr20(s string) ([20]byte, error) {
	var out [20]byte
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		return out, err
	}
	if len(b) != 20 {
		return out, fmt.Errorf("expected 20 bytes, got %d", len(b))
	}
	copy(out[:], b)
	return out, nil
}

func hexBytes(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return nil, nil
	}
	return hex.DecodeString(s)
}

func num(s string) (uint64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseUint(s, 10, 64)
}

func bigint(s string) (*big.Int, error) {
	if s == "" {
		return big.NewInt(0), nil
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("%q is not a decimal integer", s)
	}
	return v, nil
}
