// Copyright 2022-2023 VMware, Inc.
//
// This product is licensed to you under the BSD-2 license (the "License").
// You may not use this product except in compliance with the BSD-2 License.
// This product may include a number of subcomponents with separate copyright
// notices and license terms. Your use of these subcomponents is subject to
// the terms and conditions of the subcomponent's license, as noted in the
// LICENSE file.
//
// SPDX-License-Identifier: BSD-2-Clause

package metadata

import (
	"bytes"
	"crypto"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/secure-systems-lab/go-securesystemslib/cjson"
	"github.com/sigstore/sigstore/pkg/signature"
	log "github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
)

// Root return new metadata instance of type Root
func Root(expires ...time.Time) *Metadata[RootType] {
	// expire now if there's nothing set
	if len(expires) == 0 {
		expires = []time.Time{time.Now().UTC()}
	}
	// populate Roles
	roles := map[string]*Role{}
	for _, r := range []string{ROOT, SNAPSHOT, TARGETS, TIMESTAMP} {
		roles[r] = &Role{
			KeyIDs:    []string{},
			Threshold: 1,
		}
	}
	log.Debugf("Created a metadata of type %s expiring at %s\n", ROOT, expires[0])
	return &Metadata[RootType]{
		Signed: RootType{
			Type:               ROOT,
			SpecVersion:        SPECIFICATION_VERSION,
			Version:            1,
			Expires:            expires[0],
			Keys:               map[string]*Key{},
			Roles:              roles,
			ConsistentSnapshot: true,
		},
		Signatures: []Signature{},
	}
}

// Snapshot return new metadata instance of type Snapshot
func Snapshot(expires ...time.Time) *Metadata[SnapshotType] {
	// expire now if there's nothing set
	if len(expires) == 0 {
		expires = []time.Time{time.Now().UTC()}
	}
	log.Debugf("Created a metadata of type %s expiring at %s\n", SNAPSHOT, expires[0])
	return &Metadata[SnapshotType]{
		Signed: SnapshotType{
			Type:        SNAPSHOT,
			SpecVersion: SPECIFICATION_VERSION,
			Version:     1,
			Expires:     expires[0],
			Meta: map[string]MetaFiles{
				"targets.json": {
					Version: 1,
				},
			},
		},
		Signatures: []Signature{},
	}
}

// Timestamp return new metadata instance of type Timestamp
func Timestamp(expires ...time.Time) *Metadata[TimestampType] {
	// expire now if there's nothing set
	if len(expires) == 0 {
		expires = []time.Time{time.Now().UTC()}
	}
	log.Debugf("Created a metadata of type %s expiring at %s\n", TIMESTAMP, expires[0])
	return &Metadata[TimestampType]{
		Signed: TimestampType{
			Type:        TIMESTAMP,
			SpecVersion: SPECIFICATION_VERSION,
			Version:     1,
			Expires:     expires[0],
			Meta: map[string]MetaFiles{
				"snapshot.json": {
					Version: 1,
				},
			},
		},
		Signatures: []Signature{},
	}
}

// Targets return new metadata instance of type Targets
func Targets(expires ...time.Time) *Metadata[TargetsType] {
	// expire now if there's nothing set
	if len(expires) == 0 {
		expires = []time.Time{time.Now().UTC()}
	}
	log.Debugf("Created a metadata of type %s expiring at %s\n", TARGETS, expires[0])
	return &Metadata[TargetsType]{
		Signed: TargetsType{
			Type:        TARGETS,
			SpecVersion: SPECIFICATION_VERSION,
			Version:     1,
			Expires:     expires[0],
			Targets:     map[string]TargetFiles{},
		},
		Signatures: []Signature{},
	}
}

// TargetFile return new metadata instance of type TargetFiles
func TargetFile() *TargetFiles {
	return &TargetFiles{
		Length: 0,
		Hashes: Hashes{},
	}
}

// MetaFile return new metadata instance of type MetaFile
func MetaFile(version int64) *MetaFiles {
	if version < 1 {
		// attempting to set incorrect version
		log.Debugf("Attempting to set incorrect version of %d for MetaFile\n", version)
		version = 1
	}
	return &MetaFiles{
		Length:  0,
		Hashes:  Hashes{},
		Version: version,
	}
}

// FromFile load metadata from file
func (meta *Metadata[T]) FromFile(name string) (*Metadata[T], error) {
	in, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	data, err := io.ReadAll(in)
	if err != nil {
		return nil, err
	}
	m, err := fromBytes[T](data)
	if err != nil {
		return nil, err
	}
	*meta = *m
	log.Debugf("Loaded metadata from file %s\n", name)
	return meta, nil
}

// FromBytes deserialize metadata from bytes
func (meta *Metadata[T]) FromBytes(data []byte) (*Metadata[T], error) {
	m, err := fromBytes[T](data)
	if err != nil {
		return nil, err
	}
	*meta = *m
	log.Debug("Loaded metadata from bytes")
	return meta, nil
}

// ToBytes serialize metadata to bytes
func (meta *Metadata[T]) ToBytes(pretty bool) ([]byte, error) {
	log.Debug("Writing metadata to bytes")
	if pretty {
		return json.MarshalIndent(*meta, "", "\t")
	}
	return json.Marshal(*meta)
}

// ToFile save metadata to file
func (meta *Metadata[T]) ToFile(name string, pretty bool) error {
	log.Debugf("Writing metadata to file %s\n", name)
	data, err := meta.ToBytes(pretty)
	if err != nil {
		return err
	}
	return os.WriteFile(name, data, 0644)
}

// Sign create signature over Signed and assign it to Signatures
func (meta *Metadata[T]) Sign(signer signature.Signer) (*Signature, error) {
	// encode the Signed part to canonical JSON so signatures are consistent
	payload, err := cjson.EncodeCanonical(meta.Signed)
	if err != nil {
		return nil, err
	}
	// sign the Signed part
	sb, err := signer.SignMessage(bytes.NewReader(payload))
	if err != nil {
		return nil, ErrUnsignedMetadata{Msg: "problem signing metadata"}
	}
	// get the signer's PublicKey
	publ, err := signer.PublicKey()
	if err != nil {
		return nil, err
	}
	// convert to TUF Key type to get keyID
	key, err := KeyFromPublicKey(publ)
	if err != nil {
		return nil, err
	}
	// build signature
	sig := &Signature{
		KeyID:     key.ID(),
		Signature: sb,
	}
	// update the Signatures part
	meta.Signatures = append(meta.Signatures, *sig)
	// return the new signature
	log.Infof("Signed metadata with key ID: %s\n", key.ID())
	return sig, nil
}

// VerifyDelegate verifies that “delegated_metadata“ is signed with the required
// threshold of keys for the delegated role “delegated_role“
func (meta *Metadata[T]) VerifyDelegate(delegated_role string, delegated_metadata any) error {
	var keys map[string]*Key
	var roleKeyIDs []string
	var roleThreshold int
	signing_keys := map[string]bool{}
	i := any(meta)
	log.Debugf("Verifying %s\n", delegated_role)
	// collect keys, keyIDs and threshold based on delegator type
	switch i := i.(type) {
	case *Metadata[RootType]:
		keys = i.Signed.Keys
		if role, ok := (*i).Signed.Roles[delegated_role]; ok {
			roleKeyIDs = role.KeyIDs
			roleThreshold = role.Threshold
		} else {
			return ErrValue{Msg: fmt.Sprintf("no delegation found for %s", delegated_role)}
		}
	case *Metadata[TargetsType]:
		keys = i.Signed.Delegations.Keys
		for _, v := range i.Signed.Delegations.Roles {
			if v.Name == delegated_role {
				roleKeyIDs = v.KeyIDs
				roleThreshold = v.Threshold
				break
			}
		}
	default:
		return ErrType{Msg: "call is valid only on delegator metadata (should be either root or targets)"}
	}
	// if there are no keyIDs for that role it means there's no delegation found
	if len(roleKeyIDs) == 0 {
		return ErrValue{Msg: fmt.Sprintf("no delegation found for %s", delegated_role)}
	}
	// loop through each role keyID
	for _, v := range roleKeyIDs {
		sign := Signature{}
		var payload []byte
		// convert to a PublicKey type
		key, err := keys[v].ToPublicKey()
		if err != nil {
			return err
		}
		// use corresponding hash function for key type
		hash := crypto.Hash(0)
		if keys[v].Type != KeyTypeEd25519 {
			hash = crypto.SHA256
		}
		// load a verifier based on that key
		verifier, err := signature.LoadVerifier(key, hash)
		if err != nil {
			return err
		}
		// collect the signature for that key and build the payload we'll verify
		// based on the Signed part of the delegated metadata
		switch d := delegated_metadata.(type) {
		case *Metadata[RootType]:
			for _, s := range d.Signatures {
				if s.KeyID == v {
					sign = s
				}
			}
			payload, err = cjson.EncodeCanonical(d.Signed)
			if err != nil {
				return err
			}
		case *Metadata[SnapshotType]:
			for _, s := range d.Signatures {
				if s.KeyID == v {
					sign = s
				}
			}
			payload, err = cjson.EncodeCanonical(d.Signed)
			if err != nil {
				return err
			}
		case *Metadata[TimestampType]:
			for _, s := range d.Signatures {
				if s.KeyID == v {
					sign = s
				}
			}
			payload, err = cjson.EncodeCanonical(d.Signed)
			if err != nil {
				return err
			}
		case *Metadata[TargetsType]:
			for _, s := range d.Signatures {
				if s.KeyID == v {
					sign = s
				}
			}
			payload, err = cjson.EncodeCanonical(d.Signed)
			if err != nil {
				return err
			}
		default:
			return ErrType{Msg: "unknown delegated metadata type"}
		}
		// verify if the signature for that payload corresponds to the given key
		if err := verifier.VerifySignature(bytes.NewReader(sign.Signature), bytes.NewReader(payload)); err != nil {
			// failed to verify the metadata with that key ID
			log.Debugf("Failed to verify %s with key ID %s\n", delegated_role, v)
		} else {
			// save the verified keyID only if verification passed
			signing_keys[v] = true
			log.Debugf("Verified %s with key ID %s\n", delegated_role, v)
		}
	}
	// check if the amount of valid signatures is enough
	if len(signing_keys) < roleThreshold {
		log.Infof("Verifying %s failed, not enough signatures, got %d, want %d\n", delegated_role, len(signing_keys), roleThreshold)
		return ErrUnsignedMetadata{Msg: fmt.Sprintf("Verifying %s failed, not enough signatures, got %d, want %d", delegated_role, len(signing_keys), roleThreshold)}
	}
	log.Infof("Verified %s successfully\n", delegated_role)
	return nil
}

// IsExpired returns true if metadata is expired.
// It checks if referenceTime is after Signed.Expires
func (signed *RootType) IsExpired(referenceTime time.Time) bool {
	return referenceTime.After(signed.Expires)
}

// IsExpired returns true if metadata is expired.
// It checks if referenceTime is after Signed.Expires
func (signed *SnapshotType) IsExpired(referenceTime time.Time) bool {
	return referenceTime.After(signed.Expires)
}

// IsExpired returns true if metadata is expired.
// It checks if referenceTime is after Signed.Expires
func (signed *TimestampType) IsExpired(referenceTime time.Time) bool {
	return referenceTime.After(signed.Expires)
}

// IsExpired returns true if metadata is expired.
// It checks if referenceTime is after Signed.Expires
func (signed *TargetsType) IsExpired(referenceTime time.Time) bool {
	return referenceTime.After(signed.Expires)
}

// VerifyLengthHashes checks whether the MetaFiles data matches its corresponding
// length and hashes
func (f *MetaFiles) VerifyLengthHashes(data []byte) error {
	// hashes and length are optional for MetaFiles
	if len(f.Hashes) > 0 {
		err := verifyHashes(data, f.Hashes)
		if err != nil {
			return err
		}
	}
	if f.Length != 0 {
		err := verifyLength(data, f.Length)
		if err != nil {
			return err
		}
	}
	return nil
}

// VerifyLengthHashes checks whether the TargetFiles data matches its corresponding
// length and hashes
func (f *TargetFiles) VerifyLengthHashes(data []byte) error {
	err := verifyHashes(data, f.Hashes)
	if err != nil {
		return err
	}
	err = verifyLength(data, f.Length)
	if err != nil {
		return err
	}
	return nil
}

// FromFile generate TargetFiles from file
func (t *TargetFiles) FromFile(localPath string, hashes ...string) (*TargetFiles, error) {
	log.Debugf("Generating target file from file %s\n", localPath)
	// open file
	in, err := os.Open(localPath)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	// read file
	data, err := io.ReadAll(in)
	if err != nil {
		return nil, err
	}
	return t.FromBytes(localPath, data, hashes...)
}

// FromBytes generate TargetFiles from bytes
func (t *TargetFiles) FromBytes(localPath string, data []byte, hashes ...string) (*TargetFiles, error) {
	log.Debugf("Generating target file from bytes %s\n", localPath)
	var hasher hash.Hash
	targetFile := &TargetFiles{
		Hashes: map[string]HexBytes{},
	}
	// use default hash algorithm if not set
	if len(hashes) == 0 {
		hashes = []string{"sha256"}
	}
	// calculate length
	len, err := io.Copy(io.Discard, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	targetFile.Length = len
	for _, v := range hashes {
		switch v {
		case "sha256":
			hasher = sha256.New()
		case "sha512":
			hasher = sha512.New()
		default:
			return nil, ErrValue{Msg: fmt.Sprintf("failed generating TargetFile - unsupported hashing algorithm - %s", v)}
		}
		_, err := hasher.Write(data)
		if err != nil {
			return nil, err
		}
		targetFile.Hashes[v] = hasher.Sum(nil)
	}
	targetFile.Path = localPath
	return targetFile, nil
}

// ClearSignatures clears Signatures
func (meta *Metadata[T]) ClearSignatures() {
	log.Debug("Cleared signatures")
	meta.Signatures = []Signature{}
}

// IsDelegatedPath determines whether the given "targetFilepath" is in one of
// the paths that "DelegatedRole" is trusted to provide
func (role *DelegatedRole) IsDelegatedPath(targetFilepath string) (bool, error) {
	if len(role.PathHashPrefixes) > 0 {
		// TODO: handle succinct roles
		return false, nil
	} else if len(role.Paths) > 0 {
		for _, pathPattern := range role.Paths {
			return filepath.Match(targetFilepath, pathPattern)
		}
	}
	return false, nil
}

// GetRolesForTarget return the names and terminating status of all
// delegated roles who are responsible for targetFilepath
func (role *Delegations) GetRolesForTarget(targetFilepath string) map[string]bool {
	res := map[string]bool{}
	if len(role.Roles) > 0 {
		for _, r := range role.Roles {
			ok, err := r.IsDelegatedPath(targetFilepath)
			if err == nil && ok {
				res[r.Name] = r.Terminating
			}
		}
	}
	// TODO: handle succinct roles
	return res
}

// fromBytes return a *Metadata[T] object from bytes and verifies
// that the data corresponds to the caller struct type
func fromBytes[T Roles](data []byte) (*Metadata[T], error) {
	meta := &Metadata[T]{}
	// verify that the type we used to create the object is the same as the type of the metadata file
	if err := checkType[T](data); err != nil {
		return nil, err
	}
	// if all is okay, unmarshal meta to the desired Metadata[T] type
	if err := json.Unmarshal(data, meta); err != nil {
		return nil, err
	}
	// Make sure signature key IDs are unique
	if err := checkUniqueSignatures(*meta); err != nil {
		return nil, err
	}
	return meta, nil
}

// checkUniqueSignatures verifies if the signature key IDs are unique for that metadata
func checkUniqueSignatures[T Roles](meta Metadata[T]) error {
	signatures := []string{}
	for _, sig := range meta.Signatures {
		if slices.Contains(signatures, sig.KeyID) {
			return ErrValue{Msg: fmt.Sprintf("multiple signatures found for key ID %s", sig.KeyID)}
		}
		signatures = append(signatures, sig.KeyID)
	}
	return nil
}

// checkType verifies if the generic type used to create the object is the same as the type of the metadata file in bytes
func checkType[T Roles](data []byte) error {
	var m map[string]any
	i := any(new(T))
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	signedType := m["signed"].(map[string]any)["_type"].(string)
	switch i.(type) {
	case *RootType:
		if ROOT != signedType {
			return ErrValue{Msg: fmt.Sprintf("expected metadata type %s, got - %s", ROOT, signedType)}
		}
	case *SnapshotType:
		if SNAPSHOT != signedType {
			return ErrValue{Msg: fmt.Sprintf("expected metadata type %s, got - %s", SNAPSHOT, signedType)}
		}
	case *TimestampType:
		if TIMESTAMP != signedType {
			return ErrValue{Msg: fmt.Sprintf("expected metadata type %s, got - %s", TIMESTAMP, signedType)}
		}
	case *TargetsType:
		if TARGETS != signedType {
			return ErrValue{Msg: fmt.Sprintf("expected metadata type %s, got - %s", TARGETS, signedType)}
		}
	default:
		return ErrValue{Msg: fmt.Sprintf("unrecognized metadata type - %s", signedType)}
	}
	// all okay
	return nil
}

// verifyLength verifies if the passed data has the corresponding length
func verifyLength(data []byte, length int64) error {
	len, err := io.Copy(io.Discard, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if length != len {
		return ErrLengthOrHashMismatch{Msg: fmt.Sprintf("length verification failed - expected %d, got %d", length, len)}
	}
	return nil
}

// verifyHashes verifies if the hash of the passed data corresponds to it
func verifyHashes(data []byte, hashes Hashes) error {
	var hasher hash.Hash
	for k, v := range hashes {
		switch k {
		case "sha256":
			hasher = sha256.New()
		case "sha512":
			hasher = sha512.New()
		default:
			return ErrLengthOrHashMismatch{Msg: fmt.Sprintf("hash verification failed - unknown hashing algorithm - %s", k)}
		}
		hasher.Write(data)
		if hex.EncodeToString(v) != hex.EncodeToString(hasher.Sum(nil)) {
			return ErrLengthOrHashMismatch{Msg: fmt.Sprintf("hash verification failed - mismatch for algorithm %s", k)}
		}
	}
	return nil
}

// AddKey adds new signing key for delegated role "role"
// keyID: Identifier of the key to be added for “role“.
// key: Signing key to be added for “role“.
// role: Name of the role, for which “key“ is added.
func (signed *RootType) AddKey(key *Key, role string) error {
	// verify role is present
	if _, ok := signed.Roles[role]; !ok {
		return ErrValue{Msg: fmt.Sprintf("role %s doesn't exist", role)}
	}
	// add keyID to role
	if !slices.Contains(signed.Roles[role].KeyIDs, key.ID()) {
		signed.Roles[role].KeyIDs = append(signed.Roles[role].KeyIDs, key.ID())
	}
	// update Keys
	signed.Keys[key.ID()] = key // TODO: should we check if we don't accidentally override an existing keyID with another key value?
	return nil
}

// RevokeKey revoke key from “role“ and updates the Keys store.
// keyID: Identifier of the key to be removed for “role“.
// role: Name of the role, for which a signing key is removed.
func (signed *RootType) RevokeKey(keyID, role string) error {
	// verify role is present
	if _, ok := signed.Roles[role]; !ok {
		return ErrValue{Msg: fmt.Sprintf("role %s doesn't exist", role)}
	}
	// verify keyID is present for given role
	if !slices.Contains(signed.Roles[role].KeyIDs, keyID) {
		return ErrValue{Msg: fmt.Sprintf("key with id %s is not used by %s", keyID, role)}
	}
	// remove keyID from role
	filteredKeyIDs := []string{}
	for _, k := range signed.Roles[role].KeyIDs {
		if k != keyID {
			filteredKeyIDs = append(filteredKeyIDs, k)
		}
	}
	// overwrite the old keyID slice
	signed.Roles[role].KeyIDs = filteredKeyIDs
	// check if keyID is used by other roles too
	for _, r := range signed.Roles {
		if slices.Contains(r.KeyIDs, keyID) {
			return nil
		}
	}
	// delete the keyID from Keys if it's not used anywhere else
	delete(signed.Keys, keyID)
	return nil
}

// AddKey adds new signing key for delegated role "role"
// key: Signing key to be added for “role“.
// role: Name of the role, for which “key“ is added.
func (signed *TargetsType) AddKey(key *Key, role string) error {
	// check if Delegations are even present
	if signed.Delegations == nil {
		return ErrValue{Msg: fmt.Sprintf("delegated role %s doesn't exist", role)}
	}
	// loop through all delegated roles
	for i, d := range signed.Delegations.Roles {
		// if role is found
		if d.Name == role {
			// add key if keyID is not already part of keyIDs for that role
			if !slices.Contains(d.KeyIDs, key.ID()) {
				signed.Delegations.Roles[i].KeyIDs = append(signed.Delegations.Roles[i].KeyIDs, key.ID())
				signed.Delegations.Keys[key.ID()] = key // TODO: should we check if we don't accidentally override an existing keyID with another key value?
				return nil
			}
			log.Debugf("Delegated role %s already has keyID %s\n", role, key.ID())
		}
	}
	// TODO: Handle succinct roles
	return ErrValue{Msg: fmt.Sprintf("delegated role %s doesn't exist", role)}
}

// RevokeKey revokes key from delegated role "role" and updates the delegations key store
// keyID: Identifier of the key to be removed for “role“.
// role: Name of the role, for which a signing key is removed.
func (signed *TargetsType) RevokeKey(keyID string, role string) error {
	// check if Delegations are even present
	if signed.Delegations == nil {
		return ErrValue{Msg: fmt.Sprintf("delegated role %s doesn't exist", role)}
	}
	// loop through all delegated roles
	for i, d := range signed.Delegations.Roles {
		// if role is found
		if d.Name == role {
			// check if keyID is present in keyIDs for that role
			if !slices.Contains(d.KeyIDs, keyID) {
				return ErrValue{Msg: fmt.Sprintf("key with id %s is not used by %s", keyID, role)}
			}
			// remove keyID from role
			filteredKeyIDs := []string{}
			for _, k := range signed.Delegations.Roles[i].KeyIDs {
				if k != keyID {
					filteredKeyIDs = append(filteredKeyIDs, k)
				}
			}
			// overwrite the old keyID slice for that role
			signed.Delegations.Roles[i].KeyIDs = filteredKeyIDs
			// check if keyID is used by other roles too
			for _, r := range signed.Delegations.Roles {
				if slices.Contains(r.KeyIDs, keyID) {
					return nil
				}
			}
			// delete the keyID from Keys if it's not used anywhere else
			delete(signed.Delegations.Keys, keyID)
			return nil
		}
	}
	// TODO: Handle succinct roles
	return ErrValue{Msg: fmt.Sprintf("delegated role %s doesn't exist", role)}
}