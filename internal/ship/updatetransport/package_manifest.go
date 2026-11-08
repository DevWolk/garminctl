package updatetransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/hostfile"
)

const packageManifestFile = "entry.json"

type packageManifest struct {
	Version int    `json:"version"`
	Source  string `json:"source"`
	KeyID   string `json:"keyId"`
	CachedPackage
}

func validPackageID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == id
}

func newPackageManifest(target MatchRequest, release ResolvedRelease, digest, key string) (packageManifest, error) {
	if !validPackageID(digest) || !validPackageID(key) {
		return packageManifest{}, errors.New("invalid retained package digest or compiled-key identity")
	}

	release.Release.Name = ""
	release.Release.Notes = ""
	manifest := packageManifest{Version: 1, Source: releasesEndpoint, KeyID: key, Target: target, Release: release, Digest: digest}
	id, err := packageIdentity(manifest)
	manifest.ID = id

	return manifest, err
}

func packageIdentity(manifest packageManifest) (string, error) {
	identity := struct {
		Target MatchRequest `json:"target"`
		Digest string       `json:"digest"`
		KeyID  string       `json:"keyId"`
	}{manifest.Target, manifest.Digest, manifest.KeyID}

	data, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}

	digest := sha256.Sum256(data)

	return hex.EncodeToString(digest[:]), nil
}

func writePackageManifest(directory string, manifest packageManifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}

	if len(data) > maxStageRequest {
		return errors.New("package manifest exceeds the byte limit")
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}

	defer root.Close()

	file, err := root.OpenFile(packageManifestFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	if err := hostfile.Protect(filepath.Join(directory, packageManifestFile), false); err != nil {
		return err
	}

	if _, err := file.Write(data); err != nil {
		return err
	}

	if err := file.Sync(); err != nil {
		return err
	}

	return file.Close()
}

func (c *PackageCache) readManifest(ctx context.Context, id string) (packageManifest, error) {
	var manifest packageManifest
	if err := ctx.Err(); err != nil {
		return manifest, err
	}

	if !validPackageID(id) {
		return manifest, errors.New("invalid package catalog identity")
	}

	directory := filepath.Join(c.directory, id)
	if err := hostfile.Protect(directory, true); err != nil {
		return manifest, err
	}

	if err := hostfile.Protect(filepath.Join(directory, packageManifestFile), false); err != nil {
		return manifest, err
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		return manifest, err
	}

	defer root.Close()

	file, err := root.Open(packageManifestFile)
	if err != nil {
		return manifest, err
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, maxStageRequest+1))
	if err != nil {
		return manifest, err
	}

	if len(data) > maxStageRequest {
		return manifest, errors.New("package manifest exceeds the byte limit")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&manifest); err != nil {
		return manifest, err
	}

	if err := decoder.Decode(new(any)); err != io.EOF {
		return manifest, errors.New("unexpected trailing package manifest data")
	}

	if err := c.validateManifest(id, manifest); err != nil {
		return manifest, err
	}

	return manifest, ctx.Err()
}

func (c *PackageCache) validateManifest(id string, manifest packageManifest) error {
	if manifest.Version != 1 || manifest.Source != releasesEndpoint || manifest.KeyID != c.stager.key || manifest.ID != id || !validPackageID(manifest.Digest) {
		return errors.New("package manifest schema, source or compiled key does not match")
	}

	if err := validateResolved(manifest.Target, manifest.Release); err != nil {
		return err
	}

	computed, err := packageIdentity(manifest)
	if err != nil {
		return err
	}

	if computed != id {
		return errors.New("package identity does not match its catalog entry")
	}

	return nil
}

func copyPackagePair(ctx context.Context, source, destination string, release ResolvedRelease) error {
	root, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer root.Close()

	if err := copyRetainedFile(ctx, root, release.Package.Name, destination, release.Package.Name, release.Package.Size); err != nil {
		return err
	}

	return copyRetainedFile(ctx, root, release.Signature.Name, destination, release.Signature.Name, 64)
}
