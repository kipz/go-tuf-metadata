package metadata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/exp/slices"
)

func fromFile[T Roles](name string) (*Metadata[T], error) {
	in, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("error opening metadata file - %s", name)
	}
	defer in.Close()
	bytes, err := io.ReadAll(in)
	if err != nil {
		return nil, fmt.Errorf("error reading metadata bytes from file - %s", name)
	}
	meta, err := fromBytes[T](bytes)
	if err != nil {
		return nil, fmt.Errorf("error generating metadata from bytes - %s", name)
	}
	return meta, nil
}

func fromBytes[T Roles](bytes []byte) (*Metadata[T], error) {
	meta := &Metadata[T]{}
	// verify that the type we used to create the object is the same as the type of the metadata file
	if err := checkType[T](bytes); err != nil {
		return nil, err
	}
	// if all is okay, unmarshal meta to the desired Metadata[T] type
	if err := json.Unmarshal(bytes, meta); err != nil {
		return nil, err
	}
	// Make sure signature key IDs are unique
	if err := checkUniqueSignatures(*meta); err != nil {
		return nil, err
	}
	return meta, nil
}

// Verifies if the signature key IDs are unique for that metadata
func checkUniqueSignatures[T Roles](meta Metadata[T]) error {
	signatures := []string{}
	for _, sig := range meta.Signatures {
		if slices.Contains(signatures, sig.KeyID) {
			return fmt.Errorf("multiple signatures found for keyid %s", sig.KeyID)
		}
		signatures = append(signatures, sig.KeyID)
	}
	return nil
}

// Verifies if the Generic type used to create the object is the same as the type of the metadata file in bytes
func checkType[T Roles](bytes []byte) error {
	var m map[string]any
	i := any(new(T))
	if err := json.Unmarshal(bytes, &m); err != nil {
		return err
	}
	signedType := m["signed"].(map[string]any)["_type"].(string)
	switch i.(type) {
	case *RootType:
		if ROOT != signedType {
			return fmt.Errorf("expected type %s, got - %s", ROOT, signedType)
		}
	case *SnapshotType:
		if SNAPSHOT != signedType {
			return fmt.Errorf("expected type %s, got - %s", SNAPSHOT, signedType)
		}
	case *TimestampType:
		if TIMESTAMP != signedType {
			return fmt.Errorf("expected type %s, got - %s", TIMESTAMP, signedType)
		}
	case *TargetsType:
		if TARGETS != signedType {
			return fmt.Errorf("expected type %s, got - %s", TARGETS, signedType)
		}
	default:
		return fmt.Errorf("unrecognized metadata type - %s", signedType)
	}
	// all okay
	return nil
}

func verifyLength(data []byte, length int64) error {
	// TODO
	return nil
}

func verifyHashes(data []byte, hashes Hashes) error {
	// TODO
	return nil
}

func (b *HexBytes) UnmarshalJSON(data []byte) error {
	if len(data) < 2 || len(data)%2 != 0 || data[0] != '"' || data[len(data)-1] != '"' {
		return errors.New("tuf: invalid JSON hex bytes")
	}
	res := make([]byte, hex.DecodedLen(len(data)-2))
	_, err := hex.Decode(res, data[1:len(data)-1])
	if err != nil {
		return err
	}
	*b = res
	return nil
}

func (b HexBytes) MarshalJSON() ([]byte, error) {
	res := make([]byte, hex.EncodedLen(len(b))+2)
	res[0] = '"'
	res[len(res)-1] = '"'
	hex.Encode(res[1:], b)
	return res, nil
}

func (b HexBytes) String() string {
	return hex.EncodeToString(b)
}

func PathHexDigest(s string) string {
	b := sha256.Sum256([]byte(s))
	return hex.EncodeToString(b[:])
}
