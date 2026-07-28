// Command payload-builder seals a payment instruction for the enclave.
//
//	payload-builder -proxy https://<tunnel>.trycloudflare.com
//
// Why this exists as a service rather than as browser code: the TEE node decrypts
// with go-ethereum's crypto/ecies using ECIES_AES128_SHA256. The common
// JavaScript ECIES libraries use AES-256-GCM and a different KDF, so a payload
// sealed in the browser would decrypt to garbage inside the enclave. Doing it
// here keeps one implementation of the scheme and guarantees it matches.
//
// The plaintext never leaves this machine: the service binds to 127.0.0.1, the
// caller is the treasury owner's own browser, and the response is the ciphertext
// that would have been produced in the browser anyway.
package main

import (
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"bridgesafe-extension/internal/codec"
	"bridgesafe-extension/internal/xrpl"
	"bridgesafe-extension/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8110", "listen address (loopback only)")
	proxy := flag.String("proxy", envOr("EXT_PROXY_URL", ""), "extension proxy URL, for fetching the enclave public key")
	flag.Parse()

	if !strings.HasPrefix(*addr, "127.0.0.1:") && !strings.HasPrefix(*addr, "localhost:") {
		log.Fatalf("refusing to listen on %s: this service handles plaintext payment terms and must stay on loopback", *addr)
	}

	s := &server{proxy: strings.TrimRight(*proxy, "/"), http: &http.Client{Timeout: 15 * time.Second}}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /tee-public-key", s.handlePublicKey)
	mux.HandleFunc("POST /seal", s.handleSeal)

	log.Printf("payload-builder listening on http://%s", *addr)
	if s.proxy != "" {
		log.Printf("  enclave public key from %s", s.proxy)
	} else {
		log.Printf("  no proxy configured: /seal requires an explicit teePublicKey")
	}
	log.Fatal((&http.Server{
		Addr:              *addr,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}).ListenAndServe())
}

type server struct {
	proxy string
	http  *http.Client
}

// handlePublicKey returns the enclave's ECIES public key, so the UI can show
// which enclave a payload is being sealed to.
func (s *server) handlePublicKey(w http.ResponseWriter, _ *http.Request) {
	key, err := s.teePublicKey()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": "0x" + hex.EncodeToString(key)})
}

type sealRequest struct {
	ChainID           uint64 `json:"chainId"`
	Controller        string `json:"controller"`
	TreasuryID        string `json:"treasuryId"`
	RequestID         string `json:"requestId"`
	Destination       string `json:"destination"`
	AmountDrops       string `json:"amountDrops"`
	DestinationTag    uint32 `json:"destinationTag"`
	HasDestinationTag bool   `json:"hasDestinationTag"`
	Reference         string `json:"reference"`
	TeePublicKey      string `json:"teePublicKey"`
}

// handleSeal ABI-encodes the instruction and seals it to the enclave.
func (s *server) handleSeal(w http.ResponseWriter, r *http.Request) {
	var req sealRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("decoding request: %v", err)})
		return
	}

	// Validate before sealing. A destination the enclave will reject is much
	// cheaper to catch here than after a request has been opened on chain and
	// its instruction fee paid.
	if err := xrpl.ValidateClassicAddress(req.Destination); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("destination %q is not a valid XRPL r-address: %v", req.Destination, err)})
		return
	}
	amount, ok := new(big.Int).SetString(req.AmountDrops, 10)
	if !ok || amount.Sign() <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("amountDrops %q must be a positive integer number of drops", req.AmountDrops)})
		return
	}
	treasuryID, ok := new(big.Int).SetString(req.TreasuryID, 10)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "treasuryId must be a decimal integer"})
		return
	}
	requestID, ok := new(big.Int).SetString(req.RequestID, 10)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "requestId must be a decimal integer"})
		return
	}

	plaintext, err := codec.EncodePaymentInstruction(types.PaymentInstruction{
		ChainId:           new(big.Int).SetUint64(req.ChainID),
		Controller:        common.HexToAddress(req.Controller),
		TreasuryId:        treasuryID,
		RequestId:         requestID,
		Destination:       req.Destination,
		AmountDrops:       amount,
		DestinationTag:    req.DestinationTag,
		HasDestinationTag: req.HasDestinationTag,
		Reference:         req.Reference,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("encoding: %v", err)})
		return
	}

	var pubKey []byte
	if req.TeePublicKey != "" {
		pubKey, err = hex.DecodeString(strings.TrimPrefix(req.TeePublicKey, "0x"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "teePublicKey is not hex"})
			return
		}
	} else {
		pubKey, err = s.teePublicKey()
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}

	ciphertext, err := sealToEnclave(pubKey, plaintext)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ciphertext":    "0x" + hex.EncodeToString(ciphertext),
		"plaintextSize": len(plaintext),
		"payloadHash":   "0x" + hex.EncodeToString(hashOf(ciphertext)),
	})
}

// sealToEnclave performs ECIES exactly as the TEE node's /decrypt expects:
// go-ethereum's ecies with ECIES_AES128_SHA256 over secp256k1.
func sealToEnclave(pubKey, plaintext []byte) ([]byte, error) {
	px, py := unmarshalSecp256k1(pubKey)
	if px == nil {
		return nil, fmt.Errorf("enclave public key is not a valid secp256k1 point (%d bytes)", len(pubKey))
	}

	eciesPub := &ecies.PublicKey{
		X:      px,
		Y:      py,
		Curve:  ecies.DefaultCurve,
		Params: ecies.ECIES_AES128_SHA256,
	}
	ciphertext, err := ecies.Encrypt(rand.Reader, eciesPub, plaintext, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("sealing to the enclave: %w", err)
	}
	return ciphertext, nil
}

func unmarshalSecp256k1(pubKey []byte) (*big.Int, *big.Int) {
	curve := ecies.DefaultCurve
	switch len(pubKey) {
	case 65: // uncompressed
		x, y := elliptic.Unmarshal(curve, pubKey) //nolint:staticcheck // matches go-ethereum's own usage
		return x, y
	case 64: // raw X||Y, as the proxy reports it
		full := append([]byte{0x04}, pubKey...)
		x, y := elliptic.Unmarshal(curve, full) //nolint:staticcheck
		return x, y
	case 33: // compressed
		// Go's elliptic.UnmarshalCompressed does not decompress secp256k1 points;
		// go-ethereum ships a curve-aware decompressor for exactly this.
		pk, err := crypto.DecompressPubkey(pubKey)
		if err != nil {
			return nil, nil
		}
		return pk.X, pk.Y
	default:
		return nil, nil
	}
}

// teePublicKey fetches the enclave's encryption key from the extension proxy.
func (s *server) teePublicKey() ([]byte, error) {
	if s.proxy == "" {
		return nil, fmt.Errorf("no proxy configured: pass -proxy or set EXT_PROXY_URL")
	}
	resp, err := s.http.Get(s.proxy + "/info")
	if err != nil {
		return nil, fmt.Errorf("reading %s/info: %w", s.proxy, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s/info returned %d", s.proxy, resp.StatusCode)
	}

	var info struct {
		MachineData struct {
			PublicKey string `json:"publicKey"`
			EciesKey  string `json:"eciesKey"`
		} `json:"machineData"`
		PublicKey string `json:"publicKey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding proxy info: %w", err)
	}
	for _, candidate := range []string{info.MachineData.EciesKey, info.MachineData.PublicKey, info.PublicKey} {
		if candidate == "" {
			continue
		}
		b, err := hex.DecodeString(strings.TrimPrefix(candidate, "0x"))
		if err == nil && len(b) >= 33 {
			return b, nil
		}
	}
	return nil, fmt.Errorf("proxy /info contained no usable enclave public key")
}

func hashOf(b []byte) []byte {
	h := codec.Keccak256Hash(b)
	return h[:]
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// withCORS allows the local Next.js dev server to call this service.
// Only loopback origins are permitted — this endpoint handles plaintext terms.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
