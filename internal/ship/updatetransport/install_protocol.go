package updatetransport

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
)

// InstallCommand is the private cached CLI installer executor, never a public API.
const InstallCommand = "_application-install"

const installProtocol = 1

// The helper answers its request with Ready, or with a refusal when it will
// not accept a Commit.
const (
	installReady   = "ready"
	installRefused = "refused"
)

// installAccepted acknowledges the one Commit.
const installAccepted = "accepted"

// InstallOptions combines observed local paths/arguments with explicit DEB rollback consent.
type InstallOptions struct {
	Root    string   `json:"root"`
	Desktop string   `json:"desktop"`
	CLI     string   `json:"cli"`
	Args    []string `json:"args"`
	// Dir is the working directory of the running desktop, which relative restart arguments refer to.
	Dir            string `json:"dir,omitempty"`
	AllowDowngrade bool   `json:"allowDowngrade,omitempty"`
}

type installRequest struct {
	Protocol     int             `json:"protocol"`
	Operation    string          `json:"operation"`
	BuildVersion string          `json:"buildVersion"`
	KeyID        string          `json:"keyId"`
	OwnerPID     int             `json:"ownerPid"`
	Target       MatchRequest    `json:"target"`
	Release      ResolvedRelease `json:"release"`
	Digest       string          `json:"digest"`
	Install      InstallOptions  `json:"install"`
}

type installDecision struct {
	Protocol  int    `json:"protocol"`
	Operation string `json:"operation"`
	Commit    bool   `json:"commit"`
}

type installEvent struct {
	Protocol     int    `json:"protocol"`
	Operation    string `json:"operation"`
	BuildVersion string `json:"buildVersion"`
	KeyID        string `json:"keyId"`
	Kind         string `json:"kind"`
	Message      string `json:"message,omitempty"`
}

func readInstallFrame(reader *bufio.Reader, value any) error {
	data, err := reader.ReadSlice('\n')
	if err != nil {
		return err
	}

	if len(data) > maxStageRequest {
		return errors.New("installer frame exceeds the byte limit")
	}

	return decodeStageJSON(data, value)
}

func writeInstallFrame(output interface{ Write([]byte) (int, error) }, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	if len(data)+1 > maxStageRequest {
		return errors.New("installer frame exceeds the byte limit")
	}

	data = append(data, '\n')

	n, err := output.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}

	return err
}
