package updatetransport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
)

// StageCommand is the private CLI subprocess entry point, not an installer.
const StageCommand = "_application-stage"

const (
	stageProtocol   = 1
	maxStageRequest = 64 << 10
	maxStageFrame   = 4 << 10
	maxStageOutput  = 8 << 20
	operationMarker = ".stage-operation"
)

type stageRequest struct {
	Protocol     int             `json:"protocol"`
	Operation    string          `json:"operation"`
	BuildVersion string          `json:"buildVersion"`
	KeyID        string          `json:"keyId"`
	Target       MatchRequest    `json:"target"`
	Release      ResolvedRelease `json:"release"`
}

type stageEvent struct {
	Protocol     int    `json:"protocol"`
	Operation    string `json:"operation"`
	Kind         string `json:"kind"`
	Written      int64  `json:"written"`
	Total        int64  `json:"total"`
	BuildVersion string `json:"buildVersion,omitempty"`
	KeyID        string `json:"keyId,omitempty"`
	Digest       string `json:"digest,omitempty"`
}

// StageProgress reports counted bytes, not permission to install.
type StageProgress struct {
	Written int64 `json:"written"`
	Total   int64 `json:"total"`
}

func keyID(key []byte) (string, error) {
	if len(key) != ed25519.PublicKeySize {
		return "", errors.New("application staging requires a compiled 32-byte public key")
	}

	digest := sha256.Sum256(key)

	return hex.EncodeToString(digest[:]), nil
}

func decodeStageJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(value); err != nil {
		return err
	}

	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("staging protocol requires exactly one JSON value")
	}

	return nil
}

func readStageRequest(input io.Reader) (stageRequest, error) {
	var request stageRequest

	data, err := io.ReadAll(io.LimitReader(input, maxStageRequest+1))
	if err != nil {
		return request, err
	}

	if len(data) > maxStageRequest {
		return request, errors.New("staging request exceeds the byte limit")
	}

	if err := decodeStageJSON(data, &request); err != nil {
		return request, err
	}

	if request.Protocol != stageProtocol {
		return request, errors.New("unsupported staging protocol")
	}

	if nonce, err := hex.DecodeString(request.Operation); err != nil || len(nonce) != 32 {
		return request, errors.New("invalid staging operation identity")
	}

	return request, nil
}
